'use strict';

// One window of another computer: its picture on a canvas, and the mouse and
// keyboard of this window sent back to it.

const TEXT = {
  uk: {
    connecting: 'Підключення…',
    ended: 'Показ завершено: {0}',
    lost: 'з\'єднання втрачено',
    retry: 'Спробувати знову',
    // What the other computer says when it stops, in its own words.
    'the window was closed': 'вікно закрито',
    'the connection to the other computer was lost': 'з\'єднання з тим комп\'ютером втрачено',
  },
  en: {
    connecting: 'Connecting…',
    ended: 'The view ended: {0}',
    lost: 'the connection was lost',
    retry: 'Try again',
  },
};

const lang = (navigator.language || 'en').toLowerCase().startsWith('uk') ? 'uk' : 'en';
const t = (key, ...args) => (TEXT[lang][key] || key).replace(/\{(\d)\}/g, (m, i) => (args[i] !== undefined ? args[i] : ''));
const OS = { windows: 'Windows', darwin: 'macOS', linux: 'Linux' };

// Physical keys (KeyboardEvent.code) to the key codes edgehop sends, which
// are those of Linux. The other computer applies its own keyboard layout.
const KEY = {
  Escape: 1, Digit1: 2, Digit2: 3, Digit3: 4, Digit4: 5, Digit5: 6, Digit6: 7, Digit7: 8, Digit8: 9, Digit9: 10, Digit0: 11,
  Minus: 12, Equal: 13, Backspace: 14, Tab: 15,
  KeyQ: 16, KeyW: 17, KeyE: 18, KeyR: 19, KeyT: 20, KeyY: 21, KeyU: 22, KeyI: 23, KeyO: 24, KeyP: 25,
  BracketLeft: 26, BracketRight: 27, Enter: 28, ControlLeft: 29,
  KeyA: 30, KeyS: 31, KeyD: 32, KeyF: 33, KeyG: 34, KeyH: 35, KeyJ: 36, KeyK: 37, KeyL: 38,
  Semicolon: 39, Quote: 40, Backquote: 41, ShiftLeft: 42, Backslash: 43,
  KeyZ: 44, KeyX: 45, KeyC: 46, KeyV: 47, KeyB: 48, KeyN: 49, KeyM: 50,
  Comma: 51, Period: 52, Slash: 53, ShiftRight: 54, NumpadMultiply: 55, AltLeft: 56, Space: 57, CapsLock: 58,
  F1: 59, F2: 60, F3: 61, F4: 62, F5: 63, F6: 64, F7: 65, F8: 66, F9: 67, F10: 68,
  NumLock: 69, ScrollLock: 70,
  Numpad7: 71, Numpad8: 72, Numpad9: 73, NumpadSubtract: 74, Numpad4: 75, Numpad5: 76, Numpad6: 77, NumpadAdd: 78,
  Numpad1: 79, Numpad2: 80, Numpad3: 81, Numpad0: 82, NumpadDecimal: 83,
  IntlBackslash: 86, F11: 87, F12: 88,
  NumpadEnter: 96, ControlRight: 97, NumpadDivide: 98, PrintScreen: 99, AltRight: 100,
  Home: 102, ArrowUp: 103, PageUp: 104, ArrowLeft: 105, ArrowRight: 106, End: 107, ArrowDown: 108, PageDown: 109,
  Insert: 110, Delete: 111, Pause: 119, MetaLeft: 125, MetaRight: 126, ContextMenu: 127,
};

// Mouse buttons as the browser numbers them, to edgehop's numbers.
const BUTTON = { 0: 1, 1: 3, 2: 2, 3: 4, 4: 5 };

// Kinds of input events, as the local edgehop expects them.
const EV = { pointer: 1, button: 2, wheel: 3, key: 4, release: 5, ack: 6 };

const params = new URLSearchParams(location.search);
const peer = params.get('peer') || '';
const win = params.get('win') || '';
const canvas = document.getElementById('screen');
const ctx = canvas.getContext('2d');
const statusBox = document.getElementById('status');

let sid = '';        // set while the window is being shown
let endReason = '';
let fitted = false;

function setStatus(text, retry) {
  statusBox.classList.toggle('off', !text);
  document.getElementById('status-text').textContent = text || '';
  document.getElementById('retry').hidden = !retry;
}

// ---------------------------------------------------------------------------
// Input: events are queued and sent in order, one request at a time. Cursor
// positions are the exception: only the newest one is worth sending.

let queue = [];
let pointer = null;
let posting = false;

async function flush() {
  if (posting) return;
  posting = true;
  try {
    while (sid && (queue.length > 0 || pointer)) {
      if (pointer) { queue.push(pointer); pointer = null; }
      const batch = queue;
      queue = [];
      const resp = await fetch('/api/input?sid=' + sid, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ev: batch }),
      });
      if (resp.status === 410) sid = '';
    }
  } catch (e) {
    // The picture stream notices a lost connection and says so.
  }
  posting = false;
}

function push(ev) {
  if (!sid) return;
  if (pointer) { queue.push(pointer); pointer = null; } // keep the order
  queue.push(ev);
  flush();
}

function at(e) {
  const r = canvas.getBoundingClientRect();
  const unit = (v, size) => Math.round(Math.min(1, Math.max(0, v / Math.max(1, size))) * 65535);
  return [unit(e.clientX - r.left, r.width), unit(e.clientY - r.top, r.height)];
}

let wheelX = 0;
let wheelY = 0;

function wireInput() {
  canvas.addEventListener('pointermove', (e) => {
    if (!sid) return;
    pointer = [EV.pointer, ...at(e)];
    flush();
  });
  canvas.addEventListener('pointerdown', (e) => {
    canvas.focus();
    try { canvas.setPointerCapture(e.pointerId); } catch (err) { /* not essential */ }
    const b = BUTTON[e.button];
    if (b) push([EV.button, ...at(e), b, 1]);
    e.preventDefault();
  });
  canvas.addEventListener('pointerup', (e) => {
    const b = BUTTON[e.button];
    if (b) push([EV.button, ...at(e), b, 0]);
    e.preventDefault();
  });
  for (const name of ['contextmenu', 'auxclick', 'dragstart']) {
    canvas.addEventListener(name, (e) => e.preventDefault());
  }
  canvas.addEventListener('wheel', (e) => {
    e.preventDefault();
    // One notch of a mouse wheel is 120 units for edgehop; browsers report
    // about 100 pixels, or 3 lines, for it. Scrolling down is negative.
    const k = e.deltaMode === 1 ? 40 : (e.deltaMode === 2 ? 800 : 1.2);
    wheelX += e.deltaX * k;
    wheelY -= e.deltaY * k;
    const dx = Math.trunc(wheelX);
    const dy = Math.trunc(wheelY);
    if (dx === 0 && dy === 0) return;
    wheelX -= dx;
    wheelY -= dy;
    const clamp = (v) => Math.max(-3000, Math.min(3000, v));
    push([EV.wheel, ...at(e), clamp(dx), clamp(dy)]);
  }, { passive: false });

  window.addEventListener('keydown', (e) => {
    const code = KEY[e.code];
    if (!code || !sid) return;
    e.preventDefault();
    push([EV.key, code, 1]);
  });
  window.addEventListener('keyup', (e) => {
    const code = KEY[e.code];
    if (!code || !sid) return;
    e.preventDefault();
    push([EV.key, code, 0]);
  });
  // A key released while another window has the focus is never reported, so
  // let go of everything whenever this window loses it.
  window.addEventListener('blur', () => push([EV.release]));
  document.addEventListener('visibilitychange', () => { if (document.hidden) push([EV.release]); });
}

// ---------------------------------------------------------------------------
// Pictures

let drawing = Promise.resolve(); // rectangles are decoded and drawn in order

function fitWindow(w, h) {
  if (fitted) return;
  fitted = true;
  try { // works in a window that a script opened; harmless elsewhere
    const dw = window.outerWidth - window.innerWidth;
    const dh = window.outerHeight - window.innerHeight;
    window.resizeTo(Math.min(screen.availWidth, w + dw), Math.min(screen.availHeight, h + dh));
  } catch (e) { /* ignore */ }
}

let lastTitle = '';

function onMeta(meta) {
  if (meta.title) lastTitle = meta.title; // a window that is closing has no title any more
  document.title = lastTitle + ' — ' + meta.name + ' (' + (OS[meta.os] || meta.os) + ')';
  drawing = drawing.then(() => {
    if (canvas.width !== meta.w || canvas.height !== meta.h) {
      canvas.width = meta.w;
      canvas.height = meta.h;
    }
    fitWindow(meta.w, meta.h);
  });
}

function onRect(payload) {
  const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
  const seq = view.getUint32(0);
  const x = view.getUint16(4);
  const y = view.getUint16(6);
  const format = view.getUint8(12);
  const last = view.getUint8(13) !== 0;
  const blob = new Blob([payload.slice(14)], { type: format === 1 ? 'image/jpeg' : 'image/png' });
  drawing = drawing.then(async () => {
    try {
      const bitmap = await createImageBitmap(blob);
      ctx.drawImage(bitmap, x, y);
      bitmap.close();
    } catch (e) { /* a picture that does not decode is skipped */ }
    if (last) {
      setStatus('');
      push([EV.ack, seq]); // drawn: the other computer may send the next one
    }
  });
}

function onMessage(type, payload) {
  const text = () => new TextDecoder().decode(payload);
  if (type === 1) sid = JSON.parse(text()).sid;
  else if (type === 2) onMeta(JSON.parse(text()));
  else if (type === 3) onRect(payload);
  else if (type === 4) endReason = text();
}

async function run() {
  setStatus(t('connecting'));
  let reason = '';
  try {
    const resp = await fetch('/api/stream?peer=' + encodeURIComponent(peer) + '&win=' + encodeURIComponent(win),
      { cache: 'no-store', credentials: 'same-origin' });
    if (!resp.ok) throw new Error((await resp.text()) || ('HTTP ' + resp.status));
    const reader = resp.body.getReader();
    let buf = new Uint8Array(0);
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      const joined = new Uint8Array(buf.length + value.length);
      joined.set(buf);
      joined.set(value, buf.length);
      buf = joined;
      let off = 0;
      while (buf.length - off >= 5) {
        const len = new DataView(buf.buffer, buf.byteOffset + off + 1, 4).getUint32(0);
        if (buf.length - off - 5 < len) break;
        onMessage(buf[off], buf.subarray(off + 5, off + 5 + len));
        off += 5 + len;
      }
      buf = buf.slice(off);
    }
  } catch (e) {
    reason = e.message;
  }
  sid = '';
  await drawing;
  const why = endReason || reason || t('lost');
  setStatus(t('ended', TEXT[lang][why] || why), true);
}

document.documentElement.lang = lang;
document.title = 'edgehop — ' + peer;
document.getElementById('retry').textContent = t('retry');
document.getElementById('retry').addEventListener('click', () => location.reload());
wireInput();
run();
