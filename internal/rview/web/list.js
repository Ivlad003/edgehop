'use strict';

// The list of windows of the other computers. Choosing one opens it in a
// window of its own.

const TEXT = {
  uk: {
    title: 'Вікна інших комп\'ютерів',
    note: 'Експериментально. Оберіть вікно — воно відкриється тут окремим вікном, а миша й клавіатура в ньому працюватимуть на тому комп\'ютері.',
    refresh: 'Оновити',
    loading: 'Завантаження…',
    noPeers: 'Зараз немає підключених комп\'ютерів, які дозволяють показувати свої вікна. Увімкніть там «Віддалені вікна» в налаштуваннях edgehop і збережіть.',
    noWindows: 'Відкритих вікон немає.',
    minimised: 'згорнуте',
    failed: 'Не вдалося отримати список: {0}',
  },
  en: {
    title: 'Windows of the other computers',
    note: 'Experimental. Choose a window: it opens here in a window of its own, and the mouse and keyboard in it work on that computer.',
    refresh: 'Refresh',
    loading: 'Loading…',
    noPeers: 'No connected computer allows showing its windows right now. Turn on “Remote windows” in the edgehop settings there and save.',
    noWindows: 'There are no open windows.',
    minimised: 'minimised',
    failed: 'Could not get the list: {0}',
  },
};

const lang = (navigator.language || 'en').toLowerCase().startsWith('uk') ? 'uk' : 'en';
const t = (key, ...args) => (TEXT[lang][key] || key).replace(/\{(\d)\}/g, (m, i) => (args[i] !== undefined ? args[i] : ''));
const OS = { windows: 'Windows', darwin: 'macOS', linux: 'Linux' };

function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const c of children) if (c) node.append(c);
  return node;
}

async function getJSON(url) {
  const resp = await fetch(url, { cache: 'no-store', credentials: 'same-origin' });
  let data = null;
  try { data = await resp.json(); } catch (e) { /* not JSON */ }
  if (!resp.ok) throw new Error((data && data.error) || ('HTTP ' + resp.status));
  return data;
}

function openWindow(peer, w) {
  const width = Math.min(screen.availWidth, Math.max(320, w.w || 1024));
  const height = Math.min(screen.availHeight, Math.max(240, w.h || 700));
  const url = 'view.html?peer=' + encodeURIComponent(peer) + '&win=' + encodeURIComponent(w.id);
  window.open(url, 'edgehop-' + peer + '-' + w.id, 'popup=yes,width=' + width + ',height=' + height);
}

async function loadWindows(peer, box, message) {
  box.replaceChildren();
  message.className = 'message';
  message.textContent = t('loading');
  try {
    const list = await getJSON('/api/windows?peer=' + encodeURIComponent(peer.name));
    if (list.error) {
      message.className = 'message problem';
      message.textContent = list.error;
      return;
    }
    const wins = list.windows || [];
    message.textContent = wins.length === 0 ? t('noWindows') : '';
    for (const w of wins) {
      const size = w.w && w.h ? w.w + '×' + w.h : t('minimised');
      box.append(el('button', { type: 'button', class: 'win', onclick: () => openWindow(peer.name, w) },
        el('span', { class: 'w-title', text: w.title }),
        el('span', { class: 'w-info', text: [w.app, size].filter(Boolean).join(' · ') })));
    }
  } catch (e) {
    message.className = 'message problem';
    message.textContent = t('failed', e.message);
  }
}

async function load() {
  document.documentElement.lang = lang;
  document.title = 'edgehop — ' + t('title');
  document.getElementById('h-title').textContent = t('title');
  document.getElementById('h-note').textContent = t('note');
  const main = document.getElementById('peers');
  main.replaceChildren();
  let peers = [];
  try {
    peers = (await getJSON('/api/peers')).peers || [];
  } catch (e) {
    main.append(el('p', { class: 'message problem', text: t('failed', e.message) }));
    return;
  }
  if (peers.length === 0) {
    main.append(el('p', { class: 'message', text: t('noPeers') }),
      el('p', { class: 'message' }, el('button', { type: 'button', text: t('refresh'), onclick: load })));
    return;
  }
  for (const peer of peers) {
    const box = el('div', { class: 'windows' });
    const message = el('p', { class: 'message' });
    main.append(el('section', { class: 'peer' },
      el('div', { class: 'peer-head' },
        el('h2', {}, peer.name + ' ', el('span', { class: 'os', text: OS[peer.os] || peer.os })),
        el('button', { type: 'button', text: t('refresh'), onclick: () => loadWindows(peer, box, message) })),
      box, message));
    loadWindows(peer, box, message);
  }
}

load();
