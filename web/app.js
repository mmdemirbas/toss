// ============================================================
// LanPane - Frontend Application
// ============================================================

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
    state.panes = (data.panes || []).sort((a, b) => b.updatedAt - a.updatedAt);
    state.devices = data.devices || [];
    state.connected = true;
  } catch (e) {
    state.connected = false;
  }
}

async function createPane(name) {
  const res = await fetch(API + '/api/panes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: name || 'Untitled',
      blocks: [{ id: genId(), type: 'text', content: '', format: '' }]
    })
  });
  const pane = await res.json();
  state.panes.unshift(pane);
  state.selectedPaneId = pane.id;
  render();
  // Focus the first block
  setTimeout(() => {
    const ta = document.querySelector('.block-textarea');
    if (ta) ta.focus();
  }, 50);
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
  // Reload after a small delay for reconnection
  setTimeout(async () => {
    await fetchStatus();
    render();
  }, 2000);
}

// ---- SSE ----
function setupEventSource() {
  if (eventSource) eventSource.close();
  eventSource = new EventSource(API + '/api/events');
  eventSource.onmessage = (e) => {
    try {
      const data = JSON.parse(e.data);
      const selectedId = state.selectedPaneId;
      state.panes = (data.panes || []).sort((a, b) => b.updatedAt - a.updatedAt);
      state.devices = data.devices || [];
      state.connected = true;
      // Preserve selection
      state.selectedPaneId = selectedId;
      renderSidebar();
      // Only re-render editor if the selected pane was updated externally
      const currentPane = getSelectedPane();
      if (currentPane) {
        const activeEl = document.activeElement;
        const isEditing = activeEl && activeEl.closest('.block');
        if (!isEditing) renderEditor();
      }
    } catch (err) { /* ignore parse errors */ }
  };
  eventSource.onerror = () => {
    state.connected = false;
    renderStatusBar();
    setTimeout(() => setupEventSource(), 3000);
  };
}

// ---- Rendering ----
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
    document.getElementById('token-input').focus();
  } else {
    el.classList.add('hidden');
  }
}

function renderSidebar() {
  // Pane list
  const list = document.getElementById('pane-list');
  list.innerHTML = state.panes.map(p => {
    const active = p.id === state.selectedPaneId ? 'active' : '';
    const icon = getPaneIcon(p);
    const time = timeAgo(p.updatedAt);
    return `<div class="pane-item ${active}" data-id="${p.id}">
      <span class="pane-item-icon">${icon}</span>
      <span class="pane-item-name">${esc(p.name || 'Untitled')}</span>
      <span class="pane-item-time">${time}</span>
    </div>`;
  }).join('');

  // Device list
  const devList = document.getElementById('device-list');
  // Hub token display
  let tokenHtml = '';
  if (state.role === 'hub' && state.token) {
    tokenHtml = `<div class="hub-token">
      <span class="hub-token-label">Code</span>
      <span class="hub-token-value">${state.token}</span>
    </div>`;
  }
  devList.innerHTML = tokenHtml + state.devices.map(d => {
    const isSelf = d.id === state.deviceId;
    return `<div class="device-item">
      <span class="device-dot"></span>
      <span class="device-name">${esc(d.name)}${isSelf ? ' (you)' : ''}</span>
      <span class="device-role">${d.role}</span>
    </div>`;
  }).join('');
}

function renderStatusBar() {
  const dot = document.querySelector('.status-dot');
  const text = document.querySelector('.status-text');
  if (state.connected) {
    dot.className = 'status-dot connected';
    text.textContent = state.role === 'hub' ? 'hub · running' : 'spoke · connected';
  } else {
    dot.className = 'status-dot error';
    text.textContent = 'disconnected';
  }
}

function renderEditor() {
  const empty = document.getElementById('empty-state');
  const editor = document.getElementById('pane-editor');
  const pane = getSelectedPane();

  if (!pane) {
    empty.classList.remove('hidden');
    editor.classList.add('hidden');
    return;
  }

  empty.classList.add('hidden');
  editor.classList.remove('hidden');

  document.getElementById('pane-name').value = pane.name || '';
  renderBlocks(pane);
}

function renderBlocks(pane) {
  const container = document.getElementById('blocks-container');
  const blocks = pane.blocks || [];
  container.innerHTML = blocks.map((block, idx) => renderBlock(block, idx)).join('');

  // Apply syntax highlighting and markdown rendering
  blocks.forEach((block, idx) => {
    if (block.type === 'code') {
      applyHighlight(block, idx);
    } else if (block.type === 'markdown') {
      applyMarkdown(block, idx);
    }
  });

  // Auto-resize textareas
  container.querySelectorAll('.block-textarea').forEach(autoResize);
}

function renderBlock(block, idx) {
  const languages = ['javascript','typescript','python','go','rust','java','kotlin','c','cpp','html','css','json','yaml','sql','bash','xml','swift','ruby','php','dart','text'];

  if (block.type === 'image') {
    return `<div class="block" data-idx="${idx}" data-id="${block.id}">
      <div class="block-toolbar">
        <span class="block-type-btn active">image</span>
        <span class="block-toolbar-spacer"></span>
        <button class="block-action-btn" onclick="copyImage('${block.fileId}')" title="Copy image">📋</button>
        <button class="block-action-btn delete" onclick="removeBlock(${idx})" title="Remove">✕</button>
      </div>
      <div class="block-image">
        <img src="/api/files/${block.fileId}" alt="${esc(block.fileName || 'image')}">
      </div>
    </div>`;
  }

  if (block.type === 'file') {
    const ext = (block.fileName || '').split('.').pop() || '?';
    const size = formatBytes(block.fileSize || 0);
    return `<div class="block" data-idx="${idx}" data-id="${block.id}">
      <div class="block-toolbar">
        <span class="block-type-btn active">file</span>
        <span class="block-toolbar-spacer"></span>
        <button class="block-action-btn delete" onclick="removeBlock(${idx})" title="Remove">✕</button>
      </div>
      <div class="block-file">
        <div class="block-file-icon">📄</div>
        <div class="block-file-info">
          <div class="block-file-name">${esc(block.fileName || 'file')}</div>
          <div class="block-file-meta">${ext.toUpperCase()} · ${size}</div>
        </div>
        <a class="block-file-download" href="/api/files/${block.fileId}" download="${esc(block.fileName)}">Download</a>
      </div>
    </div>`;
  }

  // Text / Code / Markdown
  const isCode = block.type === 'code';
  const isMd = block.type === 'markdown';
  const monoClass = isCode ? 'mono' : '';

  const langSelector = isCode ? `<select class="block-lang-select" onchange="setBlockLang(${idx}, this.value)">
    ${languages.map(l => `<option value="${l}" ${block.language === l ? 'selected' : ''}>${l}</option>`).join('')}
  </select>` : '';

  const renderedArea = (isCode || isMd) ?
    `<div class="block-rendered" id="rendered-${idx}" ondblclick="editBlock(${idx})"></div>` : '';

  return `<div class="block" data-idx="${idx}" data-id="${block.id}">
    <div class="block-toolbar">
      <button class="block-type-btn ${block.type === 'text' ? 'active' : ''}" onclick="setBlockType(${idx},'text')">text</button>
      <button class="block-type-btn ${block.type === 'code' ? 'active' : ''}" onclick="setBlockType(${idx},'code')">code</button>
      <button class="block-type-btn ${block.type === 'markdown' ? 'active' : ''}" onclick="setBlockType(${idx},'markdown')">md</button>
      ${langSelector}
      <span class="block-toolbar-spacer"></span>
      <button class="block-action-btn" onclick="copyBlock(${idx})" title="Copy">📋</button>
      <button class="block-action-btn delete" onclick="removeBlock(${idx})" title="Remove">✕</button>
    </div>
    <textarea class="block-textarea ${monoClass}" id="textarea-${idx}"
      placeholder="Type here..."
      oninput="onBlockInput(${idx}, this)"
      onfocus="onBlockFocus(${idx})"
      onblur="onBlockBlur(${idx})"
      ${(isCode || isMd) && block.content ? 'style="display:none"' : ''}
    >${esc(block.content || '')}</textarea>
    ${renderedArea}
  </div>`;
}

function applyHighlight(block, idx) {
  const rendered = document.getElementById('rendered-' + idx);
  if (!rendered || !block.content) return;
  const lang = block.language || 'text';
  try {
    const result = hljs.highlight(block.content, { language: lang, ignoreIllegals: true });
    rendered.innerHTML = `<pre><code class="hljs language-${lang}">${result.value}</code></pre>`;
    rendered.classList.remove('hidden');
  } catch (e) {
    rendered.innerHTML = `<pre><code>${esc(block.content)}</code></pre>`;
    rendered.classList.remove('hidden');
  }
}

function applyMarkdown(block, idx) {
  const rendered = document.getElementById('rendered-' + idx);
  if (!rendered || !block.content) return;
  try {
    rendered.innerHTML = marked.parse(block.content);
    // Highlight code blocks within markdown
    rendered.querySelectorAll('pre code').forEach(el => {
      hljs.highlightElement(el);
    });
    rendered.classList.remove('hidden');
  } catch (e) {
    rendered.textContent = block.content;
  }
}

// ---- Block Interactions ----
function onBlockInput(idx, textarea) {
  autoResize(textarea);
  const pane = getSelectedPane();
  if (!pane) return;
  pane.blocks[idx].content = textarea.value;
  debouncedSave(pane);
}

function onBlockFocus(idx) {
  const rendered = document.getElementById('rendered-' + idx);
  const textarea = document.getElementById('textarea-' + idx);
  if (rendered) rendered.style.display = 'none';
  if (textarea) {
    textarea.style.display = '';
    autoResize(textarea);
  }
}

function onBlockBlur(idx) {
  const pane = getSelectedPane();
  if (!pane) return;
  const block = pane.blocks[idx];
  if ((block.type === 'code' || block.type === 'markdown') && block.content) {
    const textarea = document.getElementById('textarea-' + idx);
    const rendered = document.getElementById('rendered-' + idx);
    if (textarea) textarea.style.display = 'none';
    if (rendered) {
      rendered.style.display = '';
      if (block.type === 'code') applyHighlight(block, idx);
      else applyMarkdown(block, idx);
    }
  }
}

function editBlock(idx) {
  const textarea = document.getElementById('textarea-' + idx);
  if (textarea) {
    textarea.style.display = '';
    autoResize(textarea);
    textarea.focus();
  }
}

function setBlockType(idx, type) {
  const pane = getSelectedPane();
  if (!pane) return;
  pane.blocks[idx].type = type;
  if (type === 'code' && !pane.blocks[idx].language) {
    pane.blocks[idx].language = 'javascript';
  }
  savePaneNow(pane);
  renderEditor();
}

function setBlockLang(idx, lang) {
  const pane = getSelectedPane();
  if (!pane) return;
  pane.blocks[idx].language = lang;
  debouncedSave(pane);
}

function removeBlock(idx) {
  const pane = getSelectedPane();
  if (!pane) return;
  pane.blocks.splice(idx, 1);
  if (pane.blocks.length === 0) {
    pane.blocks.push({ id: genId(), type: 'text', content: '' });
  }
  savePaneNow(pane);
  renderEditor();
}

function addBlock(type) {
  const pane = getSelectedPane();
  if (!pane) return;
  const block = { id: genId(), type, content: '' };
  if (type === 'code') block.language = 'javascript';
  pane.blocks.push(block);
  savePaneNow(pane);
  renderEditor();
  setTimeout(() => {
    const textareas = document.querySelectorAll('.block-textarea');
    if (textareas.length) textareas[textareas.length - 1].focus();
  }, 50);
}

async function copyBlock(idx) {
  const pane = getSelectedPane();
  if (!pane) return;
  const block = pane.blocks[idx];
  if (block.content) {
    await navigator.clipboard.writeText(block.content);
    showToast('Copied to clipboard');
  }
}

async function copyImage(fileId) {
  try {
    const res = await fetch('/api/files/' + fileId);
    const blob = await res.blob();
    // Clipboard API requires image/png
    let pngBlob = blob;
    if (blob.type !== 'image/png') {
      pngBlob = await convertToPng(blob);
    }
    await navigator.clipboard.write([
      new ClipboardItem({ 'image/png': pngBlob })
    ]);
    showToast('Image copied to clipboard');
  } catch (e) {
    showToast('Copy failed — try right-click → Copy');
  }
}

function convertToPng(blob) {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      const canvas = document.createElement('canvas');
      canvas.width = img.width;
      canvas.height = img.height;
      canvas.getContext('2d').drawImage(img, 0, 0);
      canvas.toBlob(resolve, 'image/png');
    };
    img.src = URL.createObjectURL(blob);
  });
}

// ---- Save Logic ----
function debouncedSave(pane) {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => savePaneNow(pane), 400);
}

async function savePaneNow(pane) {
  clearTimeout(saveTimer);
  try { await updatePane(pane); } catch (e) { console.error('Save failed:', e); }
}

// ---- Event Listeners ----
function setupListeners() {
  // New pane button
  document.getElementById('new-pane-btn').addEventListener('click', () => createPane());

  // Pane list click
  document.getElementById('pane-list').addEventListener('click', (e) => {
    const item = e.target.closest('.pane-item');
    if (item) {
      state.selectedPaneId = item.dataset.id;
      render();
    }
  });

  // Pane name edit
  document.getElementById('pane-name').addEventListener('input', (e) => {
    const pane = getSelectedPane();
    if (pane) {
      pane.name = e.target.value;
      debouncedSave(pane);
      renderSidebar();
    }
  });

  // Add blocks
  document.getElementById('add-text-block').addEventListener('click', () => addBlock('text'));
  document.getElementById('add-code-block').addEventListener('click', () => addBlock('code'));

  // Delete pane
  document.getElementById('delete-pane-btn').addEventListener('click', () => {
    if (state.selectedPaneId && confirm('Delete this pane?')) {
      deletePane(state.selectedPaneId);
    }
  });

  // Token submit
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

  // Global paste
  document.addEventListener('paste', handlePaste);

  // Drag and drop
  let dragCounter = 0;
  document.addEventListener('dragenter', (e) => {
    e.preventDefault();
    dragCounter++;
    document.body.classList.add('drop-active');
  });
  document.addEventListener('dragleave', (e) => {
    e.preventDefault();
    dragCounter--;
    if (dragCounter <= 0) {
      dragCounter = 0;
      document.body.classList.remove('drop-active');
    }
  });
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
      await addImageFile(file);
      return;
    }
  }
}

async function handleDrop(e) {
  e.preventDefault();
  document.body.classList.remove('drop-active');

  const files = e.dataTransfer?.files;
  if (!files || files.length === 0) return;

  // Ensure we have a pane
  if (!state.selectedPaneId) {
    await createPane('Dropped files');
  }

  for (const file of files) {
    if (file.type.startsWith('image/')) {
      await addImageFile(file);
    } else {
      await addFileBlock(file);
    }
  }
}

async function addImageFile(file) {
  if (!state.selectedPaneId) await createPane('Image');
  const pane = getSelectedPane();
  if (!pane) return;

  const result = await uploadFile(file);
  const block = {
    id: genId(),
    type: 'image',
    fileId: result.fileId,
    fileName: result.fileName,
    mimeType: result.mimeType,
    fileSize: result.fileSize,
  };
  pane.blocks.push(block);
  await savePaneNow(pane);
  renderEditor();
}

async function addFileBlock(file) {
  const pane = getSelectedPane();
  if (!pane) return;

  const result = await uploadFile(file);
  const block = {
    id: genId(),
    type: 'file',
    fileId: result.fileId,
    fileName: result.fileName,
    mimeType: result.mimeType,
    fileSize: result.fileSize,
  };
  pane.blocks.push(block);
  await savePaneNow(pane);
  renderEditor();
}

// ---- Helpers ----
function getSelectedPane() {
  return state.panes.find(p => p.id === state.selectedPaneId) || null;
}

function getPaneIcon(pane) {
  if (!pane.blocks || pane.blocks.length === 0) return '📝';
  const types = pane.blocks.map(b => b.type);
  if (types.includes('image')) return '🖼';
  if (types.includes('file')) return '📎';
  if (types.includes('code')) return '⟨⟩';
  if (types.includes('markdown')) return '◈';
  return '📝';
}

function genId() {
  return Math.random().toString(36).substr(2, 12);
}

function esc(str) {
  const d = document.createElement('div');
  d.textContent = str;
  return d.innerHTML;
}

function autoResize(el) {
  if (!el) return;
  el.style.height = 'auto';
  el.style.height = el.scrollHeight + 'px';
}

function timeAgo(ts) {
  if (!ts) return '';
  const diff = Date.now() - ts;
  if (diff < 60000) return 'now';
  if (diff < 3600000) return Math.floor(diff / 60000) + 'm';
  if (diff < 86400000) return Math.floor(diff / 3600000) + 'h';
  return Math.floor(diff / 86400000) + 'd';
}

function formatBytes(bytes) {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

function showToast(msg) {
  let toast = document.querySelector('.copy-toast');
  if (!toast) {
    toast = document.createElement('div');
    toast.className = 'copy-toast';
    document.body.appendChild(toast);
  }
  toast.textContent = msg;
  toast.classList.add('show');
  setTimeout(() => toast.classList.remove('show'), 2000);
}

// ---- Start ----
document.addEventListener('DOMContentLoaded', init);
