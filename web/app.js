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
};

let saveTimer = null;
let eventSource = null;
let isEditing = false; // tracks if user is actively typing

// ---- Init ----
async function init() {
  await fetchStatus();
  setupEventSource();
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
    type: opts.type || 'markdown',
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
  if (state.selectedPaneId === id) state.selectedPaneId = null;
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
      // Update editor only if not actively editing
      if (!isEditing) {
        const pane = getSelectedPane();
        if (pane) syncEditorContent(pane);
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
    const icon = p.type === 'code' ? '⟨⟩' : '◈';
    const name = p.name || 'Untitled';
    const time = timeAgo(p.updatedAt);
    return `<div class="pane-item ${active}" data-id="${p.id}">
      <span class="pane-item-icon">${icon}</span>
      <span class="pane-item-name">${esc(name)}</span>
      <span class="pane-item-time">${time}</span>
    </div>`;
  }).join('');

  // Hub token
  const tokenArea = document.getElementById('hub-token-area');
  if (state.role === 'hub' && state.token) {
    tokenArea.innerHTML = `<div class="hub-token">
      <span class="hub-token-label">Code</span>
      <span class="hub-token-value">${state.token}</span>
    </div>`;
  } else {
    tokenArea.innerHTML = '';
  }

  // Devices
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
    text.textContent = state.role === 'hub' ? 'hub' : 'spoke · connected';
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

  // Name
  const nameInput = document.getElementById('pane-name');
  if (document.activeElement !== nameInput) {
    nameInput.value = pane.name || '';
  }

  // Mode buttons
  document.querySelectorAll('.mode-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.mode === pane.type);
  });

  // Language selector
  const langSel = document.getElementById('lang-select');
  langSel.classList.toggle('hidden', pane.type !== 'code');
  if (pane.type === 'code') {
    langSel.value = pane.language || 'plaintext';
  }

  // Editor & preview
  const editor = document.getElementById('editor');
  const preview = document.getElementById('preview');

  if (pane.type === 'code') {
    // Code mode: always show textarea
    editor.style.display = '';
    preview.classList.add('hidden');
    if (document.activeElement !== editor) {
      editor.value = pane.content || '';
    }
  } else {
    // Markdown mode
    if (isEditing) {
      // Editing: show textarea
      editor.style.display = '';
      preview.classList.add('hidden');
      if (document.activeElement !== editor) {
        editor.value = pane.content || '';
      }
    } else if (pane.content && pane.content.trim()) {
      // Has content, show rendered preview
      editor.style.display = 'none';
      preview.classList.remove('hidden');
      renderMarkdown(pane.content, preview);
    } else {
      // Empty, show textarea for input
      editor.style.display = '';
      preview.classList.add('hidden');
      editor.value = '';
    }
  }
}

function syncEditorContent(pane) {
  const editor = document.getElementById('editor');
  if (document.activeElement !== editor) {
    editor.value = pane.content || '';
  }
  if (pane.type === 'markdown' && !isEditing && pane.content && pane.content.trim()) {
    const preview = document.getElementById('preview');
    editor.style.display = 'none';
    preview.classList.remove('hidden');
    renderMarkdown(pane.content, preview);
  }
}

function renderMarkdown(content, el) {
  try {
    el.innerHTML = marked.parse(content);
    el.querySelectorAll('pre code').forEach(block => {
      hljs.highlightElement(block);
    });
  } catch (e) {
    el.textContent = content;
  }
}

// ---- Listeners ----
function setupListeners() {
  // New pane
  document.getElementById('new-pane-btn').addEventListener('click', () => createPane());

  // Pane list click
  document.getElementById('pane-list').addEventListener('click', (e) => {
    const item = e.target.closest('.pane-item');
    if (item) {
      isEditing = false;
      state.selectedPaneId = item.dataset.id;
      render();
    }
  });

  // Pane name
  document.getElementById('pane-name').addEventListener('input', (e) => {
    const pane = getSelectedPane();
    if (pane) {
      pane.name = e.target.value;
      debouncedSave(pane);
      renderSidebar();
    }
  });

  // Mode switch
  document.querySelectorAll('.mode-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const pane = getSelectedPane();
      if (!pane) return;
      pane.type = btn.dataset.mode;
      if (pane.type === 'code' && !pane.language) pane.language = 'plaintext';
      isEditing = false;
      savePaneNow(pane);
      renderEditor();
    });
  });

  // Language selector
  document.getElementById('lang-select').addEventListener('change', (e) => {
    const pane = getSelectedPane();
    if (pane) {
      pane.language = e.target.value;
      debouncedSave(pane);
    }
  });

  // Editor input
  const editor = document.getElementById('editor');
  editor.addEventListener('input', () => {
    const pane = getSelectedPane();
    if (pane) {
      pane.content = editor.value;
      debouncedSave(pane);
    }
  });
  editor.addEventListener('focus', () => { isEditing = true; });
  editor.addEventListener('blur', () => {
    isEditing = false;
    const pane = getSelectedPane();
    if (pane && pane.type === 'markdown') {
      renderEditor(); // switch to preview
    }
  });

  // Tab key in editor
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

  // Click preview to edit
  document.getElementById('preview').addEventListener('click', (e) => {
    // Don't switch to edit if clicking a link or image
    if (e.target.tagName === 'A' || e.target.tagName === 'IMG') return;
    const pane = getSelectedPane();
    if (pane && pane.type === 'markdown') {
      isEditing = true;
      renderEditor();
      focusEditor();
    }
  });

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
    if (e.key === 'Enter') {
      const val = e.target.value.trim();
      if (val) submitToken(val);
    }
  });

  // Global paste (image handling)
  document.addEventListener('paste', handlePaste);

  // Drag and drop
  let dragCounter = 0;
  document.addEventListener('dragenter', (e) => { e.preventDefault(); dragCounter++; document.body.classList.add('drop-active'); });
  document.addEventListener('dragleave', (e) => { e.preventDefault(); dragCounter--; if (dragCounter <= 0) { dragCounter = 0; document.body.classList.remove('drop-active'); } });
  document.addEventListener('dragover', (e) => e.preventDefault());
  document.addEventListener('drop', handleDrop);

  // Keyboard shortcuts
  document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'n') {
      e.preventDefault();
      createPane();
    }
  });
}

async function handlePaste(e) {
  const items = e.clipboardData?.items;
  if (!items) return;

  for (const item of items) {
    if (item.type.startsWith('image/')) {
      e.preventDefault();
      const file = item.getAsFile();
      if (!file) continue;

      // Upload the image
      const result = await uploadFile(file);
      const imgUrl = `/api/files/${result.fileId}`;

      // Ensure we have a pane
      let pane = getSelectedPane();
      if (!pane) {
        await createPane({ type: 'markdown', name: 'Image' });
        pane = getSelectedPane();
      }

      // Insert markdown image reference into content
      const imgMd = `![${result.fileName || 'image'}](${imgUrl})`;
      const editor = document.getElementById('editor');

      if (document.activeElement === editor) {
        // Insert at cursor
        const start = editor.selectionStart;
        const before = pane.content.substring(0, start);
        const after = pane.content.substring(editor.selectionEnd);
        pane.content = before + imgMd + '\n' + after;
        editor.value = pane.content;
        editor.selectionStart = editor.selectionEnd = start + imgMd.length + 1;
      } else {
        // Append
        pane.content = (pane.content ? pane.content + '\n\n' : '') + imgMd + '\n';
        editor.value = pane.content;
      }

      // If it was markdown and in preview mode, switch to edit briefly then save
      if (pane.type === 'markdown') {
        isEditing = true;
        renderEditor();
      }

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
    await createPane({ type: 'markdown', name: 'Files' });
    pane = getSelectedPane();
  }

  for (const file of files) {
    const result = await uploadFile(file);
    const url = `/api/files/${result.fileId}`;

    let insertion;
    if (file.type.startsWith('image/')) {
      insertion = `![${result.fileName}](${url})`;
    } else {
      insertion = `[${result.fileName}](${url})`;
    }
    pane.content = (pane.content ? pane.content + '\n\n' : '') + insertion + '\n';
  }

  isEditing = false;
  await savePaneNow(pane);
  renderEditor();
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
  setTimeout(() => {
    const editor = document.getElementById('editor');
    if (editor) { isEditing = true; editor.focus(); }
  }, 50);
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

// ---- Start ----
document.addEventListener('DOMContentLoaded', init);
