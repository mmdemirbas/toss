// LanPane Frontend
const API = '';
let state = {
  panes: [],
  devices: [],
  selectedPaneId: null,
  role: '',
  token: '',
  deviceId: '',
  needsToken: false,
  connected: false,
  previewing: false, // preview toggle
};

let autoNameTimer = null;
let saveTimer = null;
let eventSource = null;
let autoDetectTimer = null;
let highlightFrame = null;
let lastHighlightKey = '';

function scheduleAutoName(pane) {
  // Only auto-name if pane doesn't have an explicit name
  if ((pane.name || '').trim()) return;
  
  clearTimeout(autoNameTimer);
  autoNameTimer = setTimeout(() => {
    const name = generateAutoName(pane.content);
    if (name) {
      pane.name = name;
      document.getElementById('pane-name').value = name;
      debouncedSave(pane);
      renderSidebar();
    }
  }, 1500);
}

function generateAutoName(content) {
  if (!content || content.trim().length === 0) return null;
  
  const lines = content.trim().split('\n');
  const firstLine = lines[0].trim();
  
  if (!firstLine) return null;
  
  // For markdown, try to get heading
  if (firstLine.match(/^#+\s+/)) {
    return firstLine.replace(/^#+\s+/, '').substring(0, 40);
  }
  
  // For code, try to get function/class name or first meaningful line
  if (firstLine.length > 0) {
    let name = firstLine;
    // Remove common language keywords and punctuation
    name = name.replace(/^(const|let|var|function|class|def|func|public|private|protected|async|await)\s+/, '');
    name = name.replace(/[{};:=]/g, '');
    name = name.trim();
    
    if (name.length > 0 && name.length <= 40) {
      return name;
    }
  }
  
  // Fallback: use first line, truncated
  if (firstLine.length > 0) {
    return firstLine.substring(0, 40).trim();
  }
  
  return null;
}

// ---- Language detection ----
const langPatterns = [
  { lang: 'markdown',   test: s => /^#{1,6}\s|^\*\*|^\- \[|^\|.*\|$|!\[.*\]\(|^\>\s/m.test(s) },
  { lang: 'json',       test: s => { try { if (/^\s*[\[{]/.test(s)) { JSON.parse(s); return true; } } catch(e) {} return false; } },
  { lang: 'yaml',       test: s => /^[\w-]+:\s/m.test(s) && !/<\w/.test(s) && /\n[\w-]+:\s/m.test(s) },
  { lang: 'html',       test: s => /^\s*<!DOCTYPE|^\s*<html|<\/?(div|span|p|h[1-6]|body|head|script|style|link)\b/im.test(s) },
  { lang: 'xml',        test: s => /^\s*<\?xml/i.test(s) || (/^\s*<[\w:-]+/.test(s) && /<\/[\w:-]+>\s*$/.test(s.trim())) },
  { lang: 'css',        test: s => /[\w.#][\w\-.*#]*\s*\{[^}]*[:;]/.test(s) && !/\bfunction\b/.test(s) },
  { lang: 'sql',        test: s => /\b(SELECT|INSERT|UPDATE|DELETE|CREATE TABLE|ALTER|DROP|FROM|WHERE|JOIN)\b/i.test(s) },
  { lang: 'dockerfile', test: s => /^FROM\s+\S+/m.test(s) && /^(RUN|CMD|COPY|EXPOSE|WORKDIR|ENV|ENTRYPOINT)\s/m.test(s) },
  { lang: 'makefile',   test: s => /^[\w\-.]+:\s*/m.test(s) && /\t/.test(s) },
  { lang: 'bash',       test: s => /^#!\s*\/bin\/(ba)?sh/m.test(s) || (/\b(echo|export|if\s+\[|then|fi|done|for\s+\w+\s+in)\b/.test(s) && /[\$\|]/.test(s)) },
  { lang: 'go',         test: s => /^package\s+\w+/m.test(s) || (/\bfunc\s+[\w(]/.test(s) && /\b(import|fmt|error)\b/.test(s)) },
  { lang: 'rust',       test: s => /\bfn\s+\w+/.test(s) && /\b(let\s+mut|impl|pub\s+fn|use\s+\w|::)\b/.test(s) },
  { lang: 'python',     test: s => /\b(def\s+\w+|import\s+\w+|from\s+\w+\s+import|if\s+__name__)\b/.test(s) && /:$/.test(s.split('\n').find(l => /\b(def|class|if|for|while)\b/.test(l)) || '') },
  { lang: 'kotlin',     test: s => /\b(fun\s+\w+|val\s+\w+|var\s+\w+|package\s+\w+)\b/.test(s) && /\b(override|suspend|companion)\b/.test(s) },
  { lang: 'swift',      test: s => /\b(func\s+\w+|let\s+\w+|var\s+\w+|import\s+\w+)\b/.test(s) && /\b(guard|struct|enum|protocol)\b/.test(s) },
  { lang: 'java',       test: s => /\b(public|private|protected)\s+(static\s+)?(void|class|int|String)\b/.test(s) },
  { lang: 'typescript',  test: s => /\b(interface\s+\w+|type\s+\w+\s*=|:\s*(string|number|boolean|void)\b)/.test(s) },
  { lang: 'javascript', test: s => /\b(const|let|var|function|=>|require\(|import\s.*from)\b/.test(s) },
  { lang: 'php',        test: s => /<\?php|\$\w+\s*=/.test(s) },
  { lang: 'ruby',       test: s => /\b(def\s+\w+|end$|require\s+'|puts\s)/m.test(s) },
  { lang: 'toml',       test: s => /^\[[\w.]+\]\s*$/m.test(s) && /^\w+\s*=\s*/m.test(s) },
];

function detectLanguage(content) {
  if (!content || content.trim().length < 5) return null;
  const trimmed = content.trim();
  for (const { lang, test } of langPatterns) {
    try { if (test(trimmed)) return lang; } catch(e) {}
  }
  return null;
}

// ---- Init ----
async function init() {
  await fetchStatus();
  try {
    setupEventSource();
  } catch (e) {
    state.connected = false;
  }
  setupListeners();
  render();
}

// ---- API ----
async function fetchStatus() {
  try {
    const res = await fetch(API + '/api/status');
    const data = await res.json();
    state.role = data.role;
    state.token = data.token;
    state.deviceId = data.deviceId;
    state.needsToken = data.needsToken;
    state.panes = sortPanes(data.panes || []);
    state.devices = data.devices || [];
    state.connected = true;
  } catch (e) {
    state.connected = false;
  }
}

async function createPane(opts = {}) {
  const pane = {
    name: opts.name || '',
    type: 'code',
    content: opts.content || '',
    language: opts.language || 'plaintext',
  };
  const res = await fetch(API + '/api/panes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(pane)
  });
  const created = await res.json();
  state.panes.unshift(created);
  state.selectedPaneId = created.id;
  state.previewing = false;
  render();
  focusEditor();
  return created;
}

async function updatePane(pane) {
  pane.updatedAt = Date.now();
  pane.version = Date.now();
  await fetch(API + '/api/panes/' + pane.id, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(pane)
  });
}

async function deletePane(id) {
  await fetch(API + '/api/panes/' + id, { method: 'DELETE' });
  state.panes = state.panes.filter(p => p.id !== id);
  if (state.selectedPaneId === id) {
    state.selectedPaneId = null;
    state.previewing = false;
  }
  render();
}

async function uploadFile(file) {
  const form = new FormData();
  form.append('file', file);
  const res = await fetch(API + '/api/files', { method: 'POST', body: form });
  return await res.json();
}

async function submitToken(token) {
  await fetch(API + '/api/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token })
  });
  state.needsToken = false;
  setTimeout(async () => { await fetchStatus(); render(); }, 2000);
}

// ---- SSE ----
function setupEventSource() {
  if (eventSource) eventSource.close();
  eventSource = new EventSource(API + '/api/events');
  eventSource.onmessage = (e) => {
    try {
      const data = JSON.parse(e.data);
      const selId = state.selectedPaneId;
      state.panes = sortPanes(data.panes || []);
      state.devices = data.devices || [];
      state.role = data.role || state.role;
      if (data.token) state.token = data.token;
      state.connected = true;
      state.selectedPaneId = selId;
      renderSidebar();
      renderStatusBar();
      // Sync editor if not focused
      const editor = document.getElementById('editor');
      if (editor && document.activeElement !== editor) {
        const pane = getSelectedPane();
        if (pane) {
          editor.value = pane.content || '';
          if (state.previewing) renderPreview(pane);
          else renderEditorHighlight(pane);
        }
      }
    } catch (err) {}
  };
  eventSource.onerror = () => {
    state.connected = false;
    renderStatusBar();
    setTimeout(() => setupEventSource(), 3000);
  };
}

// ---- Render ----
function render() {
  renderOverlay();
  renderSidebar();
  renderEditor();
  renderStatusBar();
}

function renderOverlay() {
  const el = document.getElementById('token-overlay');
  if (state.needsToken) {
    el.classList.remove('hidden');
    setTimeout(() => document.getElementById('token-input').focus(), 50);
  } else {
    el.classList.add('hidden');
  }
}

function renderSidebar() {
  const list = document.getElementById('pane-list');
  list.innerHTML = state.panes.map(p => {
    const active = p.id === state.selectedPaneId ? 'active' : '';
    const icon = langIcon(p.language);
    const name = p.name || 'Untitled';
    const time = timeAgo(p.updatedAt);
    return `<div class="pane-item ${active}" data-id="${p.id}">
      <span class="pane-item-icon">${icon}</span>
      <span class="pane-item-name">${esc(name)}</span>
      <span class="pane-item-time">${time}</span>
    </div>`;
  }).join('');

  const tokenArea = document.getElementById('hub-token-area');
  tokenArea.innerHTML = (state.role === 'hub' && state.token)
    ? `<div class="hub-token"><span class="hub-token-label">Code</span><span class="hub-token-value">${state.token}</span></div>`
    : '';

  const devList = document.getElementById('device-list');
  devList.innerHTML = state.devices.map(d => {
    const self = d.id === state.deviceId ? ' (you)' : '';
    return `<div class="device-item">
      <span class="device-dot"></span>
      <span class="device-name">${esc(d.name)}${self}</span>
      <span class="device-role">${d.role}</span>
    </div>`;
  }).join('');
}

function renderStatusBar() {
  const dot = document.querySelector('.status-dot');
  const text = document.querySelector('.status-text');
  if (state.connected) {
    dot.className = 'status-dot connected';
    text.textContent = state.role === 'hub' ? 'hub' : 'spoke';
  } else {
    dot.className = 'status-dot error';
    text.textContent = 'disconnected';
  }
}

function renderEditor() {
  const empty = document.getElementById('empty-state');
  const view = document.getElementById('pane-view');
  const pane = getSelectedPane();

  if (!pane) {
    empty.classList.remove('hidden');
    view.classList.add('hidden');
    return;
  }

  empty.classList.add('hidden');
  view.classList.remove('hidden');

  const nameInput = document.getElementById('pane-name');
  if (document.activeElement !== nameInput) nameInput.value = pane.name || '';

  // Language dropdown
  const langSel = document.getElementById('lang-select');
  langSel.value = pane.language || 'plaintext';

  // Preview toggle
  const previewBtn = document.getElementById('preview-btn');
  previewBtn.classList.toggle('active', state.previewing);

  // Editor & preview
  const editor = document.getElementById('editor');
  const editorLayer = document.getElementById('editor-layer');
  const preview = document.getElementById('preview');

  if (state.previewing) {
    editorLayer.style.display = 'none';
    preview.classList.remove('hidden');
    renderPreview(pane);
  } else {
    editorLayer.style.display = '';
    preview.classList.add('hidden');
    if (document.activeElement !== editor) {
      editor.value = pane.content || '';
    }
    renderEditorHighlight(pane);
  }
}

function renderPreview(pane) {
  const preview = document.getElementById('preview');
  const content = pane.content || '';
  const lang = pane.language || 'plaintext';

  if (lang === 'markdown') {
    try {
      preview.innerHTML = marked.parse(content);
      preview.querySelectorAll('pre code').forEach(el => hljs.highlightElement(el));
    } catch (e) {
      preview.textContent = content;
    }
  } else if (lang === 'plaintext') {
    preview.innerHTML = `<pre style="white-space:pre-wrap;margin:0">${esc(content)}</pre>`;
  } else {
    try {
      const result = hljs.highlight(content, { language: lang, ignoreIllegals: true });
      preview.innerHTML = `<pre><code class="hljs language-${lang}">${result.value}</code></pre>`;
    } catch (e) {
      preview.innerHTML = `<pre><code>${esc(content)}</code></pre>`;
    }
  }
}

function renderEditorHighlight(pane) {
  if (highlightFrame) cancelAnimationFrame(highlightFrame);
  highlightFrame = requestAnimationFrame(() => {
    const codeEl = document.getElementById('editor-highlight-code');
    const editor = document.getElementById('editor');
    if (!codeEl || !editor || !pane) return;

    const content = editor.value || pane.content || '';
    const lang = pane.language || 'plaintext';
    const key = `${lang}\n${content}`;
    if (key === lastHighlightKey) return;
    lastHighlightKey = key;

    if (!content) {
      codeEl.className = 'hljs language-plaintext';
      codeEl.innerHTML = '';
      return;
    }

    try {
      if (lang === 'plaintext') {
        codeEl.className = 'hljs language-plaintext';
        codeEl.innerHTML = esc(content);
      } else {
        const result = hljs.highlight(content, { language: lang, ignoreIllegals: true });
        codeEl.className = `hljs language-${lang}`;
        codeEl.innerHTML = result.value;
      }
    } catch (e) {
      codeEl.className = 'hljs language-plaintext';
      codeEl.innerHTML = esc(content);
    }

    if (content.endsWith('\n')) {
      codeEl.innerHTML += '\n';
    }

    syncEditorScroll();
  });
}

function syncEditorScroll() {
  const editor = document.getElementById('editor');
  const highlight = document.getElementById('editor-highlight');
  if (!editor || !highlight) return;
  highlight.scrollTop = editor.scrollTop;
  highlight.scrollLeft = editor.scrollLeft;
}

// ---- Listeners ----
function setupListeners() {
  document.getElementById('new-pane-btn').addEventListener('click', () => createPane());

  document.getElementById('pane-list').addEventListener('click', (e) => {
    const item = e.target.closest('.pane-item');
    if (item) {
      state.selectedPaneId = item.dataset.id;
      state.previewing = false;
      render();
    }
  });

  document.getElementById('pane-name').addEventListener('input', (e) => {
    const pane = getSelectedPane();
    if (pane) {
      pane.name = e.target.value;
      debouncedSave(pane);
      renderSidebar();
    }
  });

  // Language selector
  document.getElementById('lang-select').addEventListener('change', (e) => {
    const pane = getSelectedPane();
    if (pane) {
      pane.language = e.target.value;
      debouncedSave(pane);
      if (state.previewing) renderPreview(pane);
      else renderEditorHighlight(pane);
    }
  });

  // Preview toggle
  document.getElementById('preview-btn').addEventListener('click', togglePreview);

  // Editor input
  const editor = document.getElementById('editor');
  editor.addEventListener('input', () => {
    const pane = getSelectedPane();
    if (!pane) return;
    pane.content = editor.value;
    debouncedSave(pane);
    scheduleAutoDetect(pane);
    scheduleAutoName(pane);
    renderEditorHighlight(pane);
  });

  editor.addEventListener('scroll', syncEditorScroll);

  // Tab key
  editor.addEventListener('keydown', (e) => {
    if (e.key === 'Tab') {
      e.preventDefault();
      const start = editor.selectionStart;
      const end = editor.selectionEnd;
      editor.value = editor.value.substring(0, start) + '  ' + editor.value.substring(end);
      editor.selectionStart = editor.selectionEnd = start + 2;
      editor.dispatchEvent(new Event('input'));
    }
  });

  // Preview selection allowed - no toggle on click
  // (user should use the button to exit preview, allowing copy/selection)

  // Copy
  document.getElementById('copy-btn').addEventListener('click', () => {
    const pane = getSelectedPane();
    if (pane && pane.content) {
      navigator.clipboard.writeText(pane.content);
      showToast('Copied to clipboard');
    }
  });

  // Delete
  document.getElementById('delete-btn').addEventListener('click', () => {
    if (state.selectedPaneId && confirm('Delete this pane?')) {
      deletePane(state.selectedPaneId);
    }
  });

  // Token
  document.getElementById('token-submit').addEventListener('click', () => {
    const val = document.getElementById('token-input').value.trim();
    if (val) submitToken(val);
  });
  document.getElementById('token-input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { const val = e.target.value.trim(); if (val) submitToken(val); }
  });

  // Global paste
  document.addEventListener('paste', handlePaste);

  // Drag and drop
  let dragCounter = 0;
  document.addEventListener('dragenter', (e) => { e.preventDefault(); dragCounter++; document.body.classList.add('drop-active'); });
  document.addEventListener('dragleave', (e) => { e.preventDefault(); dragCounter--; if (dragCounter <= 0) { dragCounter = 0; document.body.classList.remove('drop-active'); } });
  document.addEventListener('dragover', (e) => e.preventDefault());
  document.addEventListener('drop', handleDrop);

  // Keyboard shortcuts
  document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'n') { e.preventDefault(); createPane(); }
    if ((e.ctrlKey || e.metaKey) && e.key === 'p') {
      e.preventDefault();
      if (getSelectedPane()) togglePreview();
    }
  });
}

function togglePreview() {
  state.previewing = !state.previewing;
  renderEditor();
  if (!state.previewing) focusEditor();
}

function scheduleAutoDetect(pane) {
  clearTimeout(autoDetectTimer);
  autoDetectTimer = setTimeout(() => {
    const detected = detectLanguage(pane.content);
    if (detected && detected !== pane.language) {
      pane.language = detected;
      document.getElementById('lang-select').value = detected;
      if (!state.previewing) renderEditorHighlight(pane);
      // Don't save just for language change during typing — it'll save with next content save
    }
  }, 1500);
}

async function handlePaste(e) {
  const items = e.clipboardData?.items;
  if (!items) return;

  for (const item of items) {
    if (item.type.startsWith('image/')) {
      e.preventDefault();
      const file = item.getAsFile();
      if (!file) continue;

      const result = await uploadFile(file);
      const imgUrl = `/api/files/${result.fileId}`;

      let pane = getSelectedPane();
      if (!pane) {
        await createPane({ language: 'markdown' });
        pane = getSelectedPane();
      }

      const imgMd = `![${result.fileName || 'image'}](${imgUrl})`;
      const editor = document.getElementById('editor');

      // If in preview, switch to edit first
      if (state.previewing) {
        state.previewing = false;
        renderEditor();
      }

      if (document.activeElement === editor) {
        const start = editor.selectionStart;
        pane.content = pane.content.substring(0, start) + imgMd + '\n' + pane.content.substring(editor.selectionEnd);
        editor.value = pane.content;
        editor.selectionStart = editor.selectionEnd = start + imgMd.length + 1;
      } else {
        pane.content = (pane.content ? pane.content + '\n\n' : '') + imgMd + '\n';
        editor.value = pane.content;
      }

      // Auto-detect will likely pick markdown
      scheduleAutoDetect(pane);
      scheduleAutoName(pane);
      await savePaneNow(pane);
      showToast('Image pasted');
      return;
    }
  }
}

async function handleDrop(e) {
  e.preventDefault();
  document.body.classList.remove('drop-active');
  const files = e.dataTransfer?.files;
  if (!files || files.length === 0) return;

  let pane = getSelectedPane();
  if (!pane) {
    await createPane({ language: 'markdown' });
    pane = getSelectedPane();
  }

  if (state.previewing) {
    state.previewing = false;
    renderEditor();
  }

  for (const file of files) {
    const result = await uploadFile(file);
    const url = `/api/files/${result.fileId}`;
    const insertion = file.type.startsWith('image/')
      ? `![${result.fileName}](${url})`
      : `[📎 ${result.fileName}](${url})`;
    pane.content = (pane.content ? pane.content + '\n\n' : '') + insertion + '\n';
  }

  document.getElementById('editor').value = pane.content;
  scheduleAutoDetect(pane);
  scheduleAutoName(pane);
  await savePaneNow(pane);
  showToast(`${files.length} file(s) added`);
}

// ---- Save ----
function debouncedSave(pane) {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => savePaneNow(pane), 400);
}

async function savePaneNow(pane) {
  clearTimeout(saveTimer);
  try { await updatePane(pane); } catch (e) { console.error('Save failed:', e); }
}

// ---- Helpers ----
function getSelectedPane() {
  return state.panes.find(p => p.id === state.selectedPaneId) || null;
}

function sortPanes(panes) {
  return panes.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
}

function focusEditor() {
  setTimeout(() => { const e = document.getElementById('editor'); if (e) e.focus(); }, 50);
}

function langIcon(lang) {
  switch (lang) {
    case 'markdown': return '◈';
    case 'javascript': case 'typescript': return 'JS';
    case 'python': return 'Py';
    case 'go': return 'Go';
    case 'rust': return 'Rs';
    case 'java': case 'kotlin': return 'Jv';
    case 'html': case 'css': return '◇';
    case 'json': case 'yaml': case 'toml': return '{}';
    case 'sql': return 'Sq';
    case 'bash': return '$_';
    case 'swift': return 'Sw';
    case 'c': case 'cpp': return 'C';
    default: return '··';
  }
}

function esc(str) {
  const d = document.createElement('div');
  d.textContent = str;
  return d.innerHTML;
}

function timeAgo(ts) {
  if (!ts) return '';
  const diff = Date.now() - ts;
  if (diff < 60000) return 'now';
  if (diff < 3600000) return Math.floor(diff / 60000) + 'm';
  if (diff < 86400000) return Math.floor(diff / 3600000) + 'h';
  return Math.floor(diff / 86400000) + 'd';
}

function showToast(msg) {
  const toast = document.getElementById('toast');
  toast.textContent = msg;
  toast.classList.add('show');
  setTimeout(() => toast.classList.remove('show'), 2000);
}

document.addEventListener('DOMContentLoaded', init);
