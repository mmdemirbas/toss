// LanPane Frontend
const API = ''; // empty = same origin (requests go to the serving host)
let state = {
  panes: [],
  devices: [],
  selectedPaneId: null,
  role: '',
  token: '',
  deviceId: '',
  needsToken: false,
  connected: false,
  sidebarCollapsed: false,
  wrapEnabled: false,
  manualTitleByPaneId: {},
  pendingAutoNameByPaneId: {},
};

let autoNameTimer = null;
let saveTimer = null;
let eventSource = null;
let autoDetectTimer = null;
let highlightFrame = null;
let lastHighlightKey = '';
let draggedPaneId = null;
let paneMeta = {};
let creatingPane = false;

function scheduleAutoName(pane) {
  // Only auto-name if pane doesn't have an explicit name
  if ((pane.name || '').trim()) return;
  if (state.manualTitleByPaneId[pane.id]) return;
  
  clearTimeout(autoNameTimer);
  autoNameTimer = setTimeout(() => {
    applyAutoNameIfAllowed(pane.id);
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
  { lang: 'json',       test: s => { try { if (/^\s*[\[{]/.test(s)) { JSON.parse(s); return true; } } catch(e) {} return false; } },
  { lang: 'yaml',       test: s => /^[\w-]+:\s/m.test(s) && !/<\w/.test(s) && /\n[\w-]+:\s/m.test(s) },
  { lang: 'html',       test: s => /^\s*<!DOCTYPE|^\s*<html|<\/?(div|span|p|h[1-6]|body|head|script|style|link)\b/im.test(s) },
  { lang: 'xml',        test: s => /^\s*<\?xml/i.test(s) || (/^\s*<[\w:-]+/.test(s) && /<\/[\w:-]+>\s*$/.test(s.trim())) },
  { lang: 'css',        test: s => /[\w.#][\w\-.*#]*\s*\{[^}]*[:;]/.test(s) && !/\bfunction\b/.test(s) },
  { lang: 'sql',        test: s => /\b(SELECT|INSERT|UPDATE|DELETE|CREATE TABLE|ALTER|DROP|FROM|WHERE|JOIN)\b/i.test(s) },
  { lang: 'dockerfile', test: s => /^FROM\s+\S+/m.test(s) && /^(RUN|CMD|COPY|EXPOSE|WORKDIR|ENV|ENTRYPOINT)\s/m.test(s) },
  { lang: 'makefile',   test: s => /^[\w\-.]+:\s*/m.test(s) && /\t/.test(s) },
  { lang: 'bash',       test: s => /^#!\s*\/bin\/(ba|z)?sh/m.test(s) || /^#!\s*\/usr\/bin\/env\s+(ba|z)?sh/m.test(s) || (/\b(echo|export|if\s+\[|then|fi|done|for\s+\w+\s+in|apt-get|yum|brew|curl|wget|chmod|mkdir|sudo|cd|source|\.\/)\b/.test(s) && /[\$\|]|&&/.test(s)) || (/^#\s+\S/m.test(s) && /\b(echo|export|apt-get|yum|brew|curl|wget|chmod|mkdir|sudo|source)\b/.test(s)) },
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
  // Markdown last among ambiguous formats: # comments in code should not trigger markdown
  { lang: 'markdown',   test: s => {
    // Reject if content has shell/script indicators
    if (/^#!\s*\//m.test(s)) return false;
    if (/[\$\|]|&&/.test(s) && /\b(echo|export|cd|sudo|apt|brew|curl|wget|chmod|mkdir)\b/.test(s)) return false;
    // Require genuine markdown constructs
    const hasHeading = /^#{1,6}\s+\S/m.test(s);
    const hasBold = /\*\*\S.*\S\*\*/.test(s);
    const hasLink = /\[.+\]\(.+\)/.test(s);
    const hasCheckbox = /^\- \[[ x]\]/m.test(s);
    const hasTable = /^\|.+\|$/m.test(s);
    const hasBlockquote = /^>\s+\S/m.test(s);
    const hasCodeBlock = /^```/m.test(s);
    const indicators = [hasHeading, hasBold, hasLink, hasCheckbox, hasTable, hasBlockquote, hasCodeBlock];
    // Need at least 2 markdown indicators, or 1 strong one (link, table, checkbox, code block)
    const count = indicators.filter(Boolean).length;
    return count >= 2 || hasLink || hasCheckbox || hasTable || hasCodeBlock;
  }},
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
  loadSidebarState();
  loadViewState();
  await fetchStatus();
  try {
    setupEventSource();
  } catch (e) {
    state.connected = false;
  }
  setupListeners();
  render();
}

function loadViewState() {
  try {
    state.wrapEnabled = localStorage.getItem('lanpane.wrapEnabled') === '1';
  } catch (e) {
    state.wrapEnabled = false;
  }
  applyWrapState();
}

function loadSidebarState() {
  try {
    state.sidebarCollapsed = localStorage.getItem('lanpane.sidebarCollapsed') === '1';
  } catch (e) {
    state.sidebarCollapsed = false;
  }
  applySidebarState();
}

function applySidebarState() {
  const app = document.getElementById('app');
  const btn = document.getElementById('sidebar-toggle-btn');
  if (!app || !btn) return;

  app.classList.toggle('sidebar-collapsed', state.sidebarCollapsed);
  btn.title = state.sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar';
  btn.setAttribute('aria-label', btn.title);
}

function toggleSidebar() {
  state.sidebarCollapsed = !state.sidebarCollapsed;
  applySidebarState();
  try {
    localStorage.setItem('lanpane.sidebarCollapsed', state.sidebarCollapsed ? '1' : '0');
  } catch (e) {}
}

function applyWrapState() {
  const app = document.getElementById('app');
  const btn = document.getElementById('wrap-btn');
  if (app) app.classList.toggle('wrap-enabled', state.wrapEnabled);
  if (btn) {
    btn.classList.toggle('active', state.wrapEnabled);
    btn.title = state.wrapEnabled ? 'Disable wrap' : 'Enable wrap';
    btn.setAttribute('aria-label', btn.title);
  }
}

function toggleWrap() {
  state.wrapEnabled = !state.wrapEnabled;
  applyWrapState();
  try {
    localStorage.setItem('lanpane.wrapEnabled', state.wrapEnabled ? '1' : '0');
  } catch (e) {}

  const pane = getSelectedPane();
  if (!pane) return;
  if (isPanePreviewing(pane.id)) renderPreview(pane);
  else renderEditorHighlight(pane);
}

function applyAutoNameIfAllowed(paneId) {
  const pane = state.panes.find((p) => p.id === paneId);
  if (!pane) return;
  if ((pane.name || '').trim()) return;
  if (state.manualTitleByPaneId[paneId]) return;

  const name = generateAutoName(pane.content);
  if (!name) return;

  const nameInput = document.getElementById('pane-name');
  if (state.selectedPaneId === paneId && document.activeElement === nameInput) {
    state.pendingAutoNameByPaneId[paneId] = true;
    return;
  }

  pane.name = name;
  delete state.pendingAutoNameByPaneId[paneId];
  if (state.selectedPaneId === paneId && nameInput) {
    nameInput.value = name;
  }
  debouncedSave(pane);
  renderSidebar();
}

function isPanePreviewing(paneId) {
  const pane = state.panes.find((p) => p.id === paneId);
  if (!pane) return false;
  if (pane.preview !== undefined && pane.preview !== null) {
    return !!pane.preview;
  }
  // Default: markdown gets preview
  return pane.language === 'markdown';
}

function setPanePreviewing(paneId, previewing) {
  if (!paneId) return;
  const pane = state.panes.find((p) => p.id === paneId);
  if (pane) {
    pane.preview = !!previewing;
  }
}

function selectPane(paneId) {
  state.selectedPaneId = paneId;
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
  if (creatingPane) return null;
  creatingPane = true;
  const pane = {
    name: opts.name || '',
    type: 'code',
    content: opts.content || '',
    language: opts.language || 'plaintext',
    order: Date.now(),
  };
  try {
    const res = await fetch(API + '/api/panes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(pane)
    });
    const created = await res.json();
    state.panes = [created, ...state.panes.filter((p) => p.id !== created.id)];
    state.selectedPaneId = created.id;
    render();
    focusEditor();
    return created;
  } finally {
    creatingPane = false;
  }
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
  delete state.manualTitleByPaneId[id];
  delete state.pendingAutoNameByPaneId[id];
  if (state.selectedPaneId === id) {
    state.selectedPaneId = state.panes.length ? state.panes[0].id : null;
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
      if (typeof data.needsToken === 'boolean') state.needsToken = data.needsToken;
      state.connected = true;
      state.selectedPaneId = selId;
      renderSidebar();
      renderStatusBar();
      renderOverlay();
      // Sync editor: language, preview state, content
      renderEditor();
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
  const now = Date.now();
  const addRow = `<div class="pane-item pane-item-add" data-add-tab="1" role="button" tabindex="0" aria-label="Add Tab">
      <span class="pane-item-icon">+</span>
      <span class="pane-item-name">Add Tab</span>
    </div>`;
  list.innerHTML = addRow + state.panes.map(p => {
    const active = p.id === state.selectedPaneId ? 'active' : '';
    const icon = langIcon(p.language);
    const name = p.name || 'Untitled';
    const time = timeAgo(p.updatedAt);
    const prev = paneMeta[p.id];
    const isNew = !prev;
    const isUpdated = !!prev && (p.updatedAt || 0) > (prev.updatedAt || 0);
    const seenAt = prev ? prev.seenAt : now;
    const ageMs = now - (p.updatedAt || seenAt || now);
    let ageClass = 'age-old';
    if (ageMs < 60000) ageClass = 'age-fresh';
    else if (ageMs < 3 * 60000) ageClass = 'age-recent';
    else if (ageMs < 15 * 60000) ageClass = 'age-warm';
    else if (ageMs < 60 * 60000) ageClass = 'age-cool';
    const flashClass = isNew ? 'flash-new' : (isUpdated ? 'flash-updated' : '');
    paneMeta[p.id] = { updatedAt: p.updatedAt || 0, seenAt };

    return `<div class="pane-item ${active} ${ageClass} ${flashClass}" data-id="${p.id}" draggable="true">
      <span class="pane-item-icon">${icon}</span>
      <span class="pane-item-name">${esc(name)}</span>
      <span class="pane-item-time">${time}</span>
      <button class="pane-item-delete" data-delete-id="${p.id}" title="Delete pane" aria-label="Delete pane">x</button>
    </div>`;
  }).join('');

  const currentIds = new Set(state.panes.map((p) => p.id));
  Object.keys(paneMeta).forEach((id) => {
    if (!currentIds.has(id)) delete paneMeta[id];
  });

  const tokenArea = document.getElementById('hub-token-area');
  tokenArea.innerHTML = (state.role === 'hub' && state.token)
    ? `<div class="hub-token"><span class="hub-token-label">Code</span><span class="hub-token-value">${esc(state.token)}</span></div>`
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
  const previewing = isPanePreviewing(pane.id);
  previewBtn.classList.toggle('active', previewing);

  // Editor & preview
  const editor = document.getElementById('editor');
  const editorLayer = document.getElementById('editor-layer');
  const preview = document.getElementById('preview');
  applyWrapState();

  if (previewing) {
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
      const rawHTML = marked.parse(content);
      preview.innerHTML = typeof DOMPurify !== 'undefined' ? DOMPurify.sanitize(rawHTML) : rawHTML;
      preview.querySelectorAll('pre code').forEach(el => hljs.highlightElement(el));
      enhanceMarkdownCodeBlocks(preview);
    } catch (e) {
      preview.textContent = content;
    }
  } else if (lang === 'plaintext') {
    const ws = state.wrapEnabled ? 'pre-wrap' : 'pre';
    preview.innerHTML = `<pre style="white-space:${ws};margin:0">${esc(content)}</pre>`;
  } else {
    try {
      const result = hljs.highlight(content, { language: lang, ignoreIllegals: true });
      preview.innerHTML = `<pre><code class="hljs language-${lang}">${result.value}</code></pre>`;
    } catch (e) {
      preview.innerHTML = `<pre><code>${esc(content)}</code></pre>`;
    }
  }
}

function enhanceMarkdownCodeBlocks(previewEl) {
  previewEl.querySelectorAll('pre').forEach((pre) => {
    if (pre.querySelector('.code-copy-btn')) return;
    const code = pre.querySelector('code');
    if (!code) return;

    pre.classList.add('code-copy-ready');
    const btn = document.createElement('button');
    btn.className = 'code-copy-btn';
    btn.type = 'button';
    btn.textContent = 'Copy';
    btn.addEventListener('click', async (e) => {
      e.preventDefault();
      e.stopPropagation();
      try {
        await navigator.clipboard.writeText(code.textContent || '');
        showToast('Code copied');
      } catch (err) {
        showToast('Copy failed');
      }
    });
    pre.appendChild(btn);
  });
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
  // Compensate for textarea scrollbar so content areas match
  const sbW = editor.offsetWidth - editor.clientWidth;
  const sbH = editor.offsetHeight - editor.clientHeight;
  highlight.style.paddingRight = sbW > 0 ? (20 + sbW) + 'px' : '';
  highlight.style.paddingBottom = sbH > 0 ? (16 + sbH) + 'px' : '';
}

// ---- Listeners ----
function setupListeners() {
  document.getElementById('sidebar-toggle-btn').addEventListener('click', toggleSidebar);
  document.getElementById('wrap-btn').addEventListener('click', toggleWrap);

  document.getElementById('pane-list').addEventListener('click', (e) => {
    const addRow = e.target.closest('.pane-item-add');
    if (addRow) {
      createPane();
      return;
    }
    const delBtn = e.target.closest('.pane-item-delete');
    if (delBtn) {
      const id = delBtn.dataset.deleteId;
      if (id && confirm('Delete this pane?')) {
        deletePane(id);
      }
      return;
    }
    const item = e.target.closest('.pane-item');
    if (item) {
      selectPane(item.dataset.id);
    }
  });

  document.getElementById('pane-list').addEventListener('keydown', (e) => {
    const addRow = e.target.closest('.pane-item-add');
    if (!addRow) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      createPane();
    }
  });

  document.getElementById('pane-list').addEventListener('dragstart', (e) => {
    const item = e.target.closest('.pane-item');
    if (!item || item.classList.contains('pane-item-add')) return;
    draggedPaneId = item.dataset.id;
    item.classList.add('dragging');
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', draggedPaneId);
    }
  });

  document.getElementById('pane-list').addEventListener('dragover', (e) => {
    const item = e.target.closest('.pane-item');
    if (!item || item.classList.contains('pane-item-add') || item.dataset.id === draggedPaneId) return;
    e.preventDefault();
    e.stopPropagation();
    const rect = item.getBoundingClientRect();
    const after = e.clientY > rect.top + rect.height / 2;
    item.classList.toggle('drag-over-before', !after);
    item.classList.toggle('drag-over-after', after);
  });

  document.getElementById('pane-list').addEventListener('dragleave', (e) => {
    const item = e.target.closest('.pane-item');
    if (!item) return;
    item.classList.remove('drag-over-before', 'drag-over-after');
  });

  document.getElementById('pane-list').addEventListener('drop', (e) => {
    const item = e.target.closest('.pane-item');
    if (!item || item.classList.contains('pane-item-add') || !draggedPaneId || item.dataset.id === draggedPaneId) return;
    e.preventDefault();
    e.stopPropagation();
    const rect = item.getBoundingClientRect();
    const after = e.clientY > rect.top + rect.height / 2;
    reorderPanes(draggedPaneId, item.dataset.id, after);
    clearDragMarkers();
  });

  document.getElementById('pane-list').addEventListener('dragend', () => {
    clearDragMarkers();
    draggedPaneId = null;
  });

  document.getElementById('pane-name').addEventListener('input', (e) => {
    const pane = getSelectedPane();
    if (pane) {
      const value = e.target.value;
      pane.name = value;
      state.manualTitleByPaneId[pane.id] = value.trim().length > 0;
      if (value.trim().length > 0) delete state.pendingAutoNameByPaneId[pane.id];
      debouncedSave(pane);
      renderSidebar();
    }
  });

  document.getElementById('pane-name').addEventListener('blur', (e) => {
    const pane = getSelectedPane();
    if (!pane) return;
    const value = e.target.value.trim();
    if (value.length > 0) return;

    pane.name = '';
    state.manualTitleByPaneId[pane.id] = false;
    if (state.pendingAutoNameByPaneId[pane.id] || (pane.content || '').trim()) {
      applyAutoNameIfAllowed(pane.id);
    } else {
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
      if (isPanePreviewing(pane.id)) renderPreview(pane);
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

  // Keyboard shortcuts (only non-conflicting ones; avoid Ctrl/Cmd combos that browsers use)
  document.addEventListener('keydown', (e) => {
    if (e.altKey && e.key.toLowerCase() === 'w') {
      e.preventDefault();
      toggleWrap();
    }
  });

  // Resize: re-sync highlight overlay padding for scrollbar compensation
  window.addEventListener('resize', syncEditorScroll);
}

function clearDragMarkers() {
  document.querySelectorAll('.pane-item').forEach((el) => {
    el.classList.remove('dragging', 'drag-over-before', 'drag-over-after');
  });
}

function reorderPanes(sourceId, targetId, placeAfter) {
  const sourceIndex = state.panes.findIndex((p) => p.id === sourceId);
  const targetIndex = state.panes.findIndex((p) => p.id === targetId);
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return;

  const next = [...state.panes];
  const [moved] = next.splice(sourceIndex, 1);
  let insertIndex = targetIndex;
  if (sourceIndex < targetIndex) insertIndex -= 1;
  if (placeAfter) insertIndex += 1;
  next.splice(Math.max(0, insertIndex), 0, moved);

  const base = Date.now();
  next.forEach((pane, idx) => {
    pane.order = base - idx;
  });

  state.panes = next;
  renderSidebar();
  savePaneOrder(next);
}

async function savePaneOrder(panes) {
  try {
    await Promise.all(panes.map((pane) => updatePane(pane)));
  } catch (e) {
    console.error('Pane reorder save failed:', e);
  }
}

function togglePreview() {
  const pane = getSelectedPane();
  if (!pane) return;
  const newState = !isPanePreviewing(pane.id);
  setPanePreviewing(pane.id, newState);
  debouncedSave(pane);
  renderEditor();
  if (!newState) focusEditor();
}

function scheduleAutoDetect(pane) {
  clearTimeout(autoDetectTimer);
  autoDetectTimer = setTimeout(() => {
    const detected = detectLanguage(pane.content);
    if (detected && detected !== pane.language) {
      pane.language = detected;
      document.getElementById('lang-select').value = detected;
      debouncedSave(pane);
      if (!isPanePreviewing(pane.id)) renderEditorHighlight(pane);
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
      if (isPanePreviewing(pane.id)) {
        setPanePreviewing(pane.id, false);
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

  if (isPanePreviewing(pane.id)) {
    setPanePreviewing(pane.id, false);
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
  return panes.sort((a, b) => {
    const orderDiff = (b.order || 0) - (a.order || 0);
    if (orderDiff !== 0) return orderDiff;
    return (b.updatedAt || 0) - (a.updatedAt || 0);
  });
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
