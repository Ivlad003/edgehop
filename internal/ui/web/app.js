'use strict';

// ---------------------------------------------------------------------------
// Texts

const TEXT = {
  uk: {
    langButton: 'EN',
    lockedTitle: 'Відкрийте посилання з вікна edgehop',
    lockedText: 'З міркувань безпеки ця сторінка відкривається лише за посиланням, яке edgehop друкує під час запуску — воно містить код доступу. Скопіюйте його з вікна програми.',
    offlineTitle: 'edgehop не запущено',
    offlineText: 'Сторінка втратила зв\'язок із програмою. Запустіть edgehop знову.',
    statusTitle: 'Стан',
    start: 'Запустити', restart: 'Перезапустити', stop: 'Зупинити',
    pillStopped: 'Зупинено', pillRunning: 'Працює', pillWaiting: 'Очікування',
    needSetup: 'Спершу оберіть нижче роль цього комп\'ютера і збережіть налаштування.',
    stoppedLine: 'Спільне керування вимкнено.',
    starting: 'Запуск…',
    stoppedBecause: 'Зупинилось: {0}',
    cursorHere: 'Курсор на цьому комп\'ютері.',
    cursorOn: 'Курсор зараз на «{0}».',
    connectedCount: 'Підключено комп\'ютерів: {0} з {1}.',
    noOthers: 'Інших екранів у розкладці ще немає — додайте їх нижче.',
    clientConnected: 'Підключено до {0}. Курсор зараз на іншому комп\'ютері.',
    clientActive: 'Підключено до {0}. Курсор зараз тут.',
    clientWaiting: "Очікування сервера {0}…",
    errUnreachable: "Сервер {0} не відповідає. Перевірте адресу, що edgehop там запущено і що брандмауер сервера дозволяє його порт.",
    errBadKey: "Сервер відповідає, але ключ не збігається. Скопіюйте ключ зі сторінки edgehop на сервері.",
    errNotInLayout: "Сервер відповідає, але ще не знає екрана «{0}». Відкрийте сторінку edgehop на комп'ютері з мишею: цей комп'ютер з'явиться там угорі з кнопками «Додати ліворуч» і «Додати праворуч».",
    errSameName: "Ім'я «{0}» вже має сам сервер. Дайте цьому комп'ютеру інше ім'я.",
    errVersion: "На сервері інша версія edgehop. Оновіть edgehop на обох комп'ютерах.",
    clientNote: "Де який екран стоїть (ліворуч, праворуч, зверху, знизу), задається на комп'ютері з мишею. Цей комп'ютер там має називатись «{0}».",
    pendingText: "«{0}» ({1}) хоче підключитись, але його ще немає в розкладці. Де він стоїть відносно цього комп'ютера?",
    addLeft: "Додати ліворуч", addRight: "Додати праворуч",
    leftCap: "← ліворуч", rightCap: "праворуч →",
    above: "Екран над цим комп'ютером", below: "Екран під цим комп'ютером",
    aboveBelowHint: "Необов'язково: ім'я комп'ютера, чий екран стоїть вище або нижче.",
    hotkeyNote: '{0} повертає курсор на цей комп\'ютер.',
    thisComputer: 'цей комп\'ютер', online: 'підключено', offlineState: 'не в мережі', cursor: 'курсор',
    setupTitle: 'Налаштування',
    roleServer: 'Миша й клавіатура підключені сюди',
    roleServerText: 'Цей комп\'ютер ділиться ними з іншими (сервер).',
    roleClient: 'Цим комп\'ютером керують з іншого',
    roleClientText: 'Миша й клавіатура підключені до іншого комп\'ютера (клієнт).',
    name: 'Ім\'я цього екрана',
    nameHint: 'Коротке ім\'я без пробілів; у кожного комп\'ютера своє.',
    serverAddr: 'Адреса сервера',
    serverAddrHint: 'IP-адреса комп\'ютера з мишею — вона показана на його сторінці edgehop.',
    key: 'Ключ',
    keyHintServer: 'Спільний секрет. Той самий ключ треба ввести на кожному комп\'ютері.',
    keyHintClient: 'Скопіюйте ключ зі сторінки edgehop на сервері.',
    show: 'Показати', hide: 'Сховати', copy: 'Копіювати', copied: 'Скопійовано', generate: 'Новий ключ',
    layout: 'Екрани зліва направо',
    layoutHint: 'Розставте екрани так, як вони стоять на столі. Імена мають збігатися з іменами, заданими на тих комп\'ютерах.',
    addScreen: 'Додати екран', addPlaceholder: 'ім\'я, напр. macbook',
    moveLeft: 'Посунути ліворуч', moveRight: 'Посунути праворуч', remove: 'Прибрати',
    screenOpts: 'Параметри екранів',
    clipShare: "Спільний буфер обміну: скопійований текст і файли можна вставити на іншому комп'ютері",
    clipTitle: "Буфер обміну: обмеження",
    clipMax: "Найбільший розмір для передачі, МБ",
    clipHint: "Більший вміст не передається. Зображення (наприклад знімки екрана) поки не передаються.",
    clipLast: "Остання передача: {0}",
    sendTitle: "Надіслати файли",
    dropText: "Перетягніть сюди файли або папки — вони з'являться на іншому комп'ютері.",
    pickFiles: "Вибрати файли", pickFolder: "Вибрати папку", cancel: "Скасувати",
    sendTo: "Кому:",
    sendNoTarget: "Зараз немає підключеного комп'ютера, який приймає файли: на ньому потрібен edgehop 0.3 або новіший.",
    sendCopying: "Підготовка: файл {0} з {1}, разом {2}",
    sendSending: "Надсилання на «{0}»…",
    sendDone: "Надіслано на «{0}»: файлів — {1}, {2}.",
    sendFailed: "Не вдалося надіслати: {0}",
    sendEmpty: "Тут немає файлів, які можна надіслати.",
    sendReadError: "файл не вдалося прочитати",
    sendStopped: "edgehop зупинено",
    savedIn: "Файли, надіслані на цей комп'ютер, зберігаються в {0}",
    rwinServer: "Віддалені вікна (експериментально): показувати тут, в окремих вікнах, вікна інших комп'ютерів",
    rwinClient: "Віддалені вікна (експериментально): дозволити комп'ютеру з мишею показувати вікна цього комп'ютера й керувати ними",
    rwinHint: "Вмикається на обох комп'ютерах. Показувати можна вікна комп'ютерів з Windows і macOS.",
    rwinTitle: "Віддалені вікна (експериментально)",
    rwinOpen: "Обрати вікно",
    rwinText: "Вікно іншого комп'ютера відкривається тут окремим вікном; миша й клавіатура в ньому працюють на тому комп'ютері. Дозволили показ: {0}.",
    rwinNone: "Жоден підключений комп'ютер ще не дозволив показувати свої вікна: увімкніть там «Віддалені вікна» в налаштуваннях edgehop і збережіть.",
    rwinAllowed: "Комп'ютер з мишею може показувати вікна цього комп'ютера й керувати ними.",
    speedHint: "Швидкість курсора підбирається автоматично під розмір кожного екрана. Число нижче множить її: 1 — як є, 1.5 — у півтора раза швидше.",
    speed: 'Швидкість курсора',
    swap: 'Поміняти Alt і Win/Cmd (PC-клавіатура ↔ Mac)',
    advanced: 'Додатково',
    port: 'Порт',
    portHint: 'TCP і UDP. Його треба дозволити у брандмауері цього комп\'ютера.',
    hotkey: 'Клавіші повернення',
    hotkeyHint: 'Повертають курсор на цей комп\'ютер. Наприклад ctrl+alt+shift+backspace; none — вимкнути.',
    hideCursor: 'Ховати курсор цього комп\'ютера, поки він керує іншим',
    sizeTitle: 'Розмір екрана',
    screenSize: 'Розмір робочого столу',
    screenSizeHint: 'Залиште порожнім для автоматичного визначення. Потрібно лише тоді, коли розмір визначено неправильно (здебільшого на Linux).',
    saveStart: 'Зберегти і запустити', save: 'Лише зберегти', saved: 'Збережено',
    saveFailed: 'Не вдалося зберегти: {0}',
    actionFailed: 'Не вдалося: {0}',
    errRole: 'Оберіть роль цього комп\'ютера.',
    errName: 'Введіть ім\'я цього екрана.',
    errSpace: 'Ім\'я екрана не може містити пробілів.',
    errKey: 'Введіть ключ.',
    errServer: 'Введіть адресу сервера.',
    errDup: 'Екран «{0}» уже є в розкладці.',
    configError: 'Файл налаштувань містить помилку: {0}\nЗбережіть налаштування наново, щоб її виправити.',
    othersTitle: 'Як підключити інші комп\'ютери',
    othersStep1: 'Завантажте edgehop на інший комп\'ютер і запустіть його.',
    download: 'Завантажити',
    othersStep2: 'Оберіть там «Цим комп\'ютером керують з іншого» і введіть:',
    othersStep3: 'Або зробіть те саме однією командою:',
    addFirst: 'спершу додайте екран у розкладку вище',
    thisTitle: 'Цей комп\'ютер',
    factName: 'Ім\'я комп\'ютера', factSystem: 'Система', factDesktop: 'Робочий стіл',
    factMonitors: 'моніторів: {0}', factAddr: 'Адреси', factConfig: 'Файл налаштувань',
    logTitle: 'Журнал',
  },
  en: {
    langButton: 'УКР',
    lockedTitle: 'Open the link from the edgehop window',
    lockedText: 'For safety this page only opens through the link edgehop prints when it starts — it contains an access code. Copy it from the program window.',
    offlineTitle: 'edgehop is not running',
    offlineText: 'The page lost contact with the program. Start edgehop again.',
    statusTitle: 'Status',
    start: 'Start', restart: 'Restart', stop: 'Stop',
    pillStopped: 'Stopped', pillRunning: 'Running', pillWaiting: 'Waiting',
    needSetup: 'First choose this computer\'s role below and save the settings.',
    stoppedLine: 'Sharing is off.',
    starting: 'Starting…',
    stoppedBecause: 'It stopped: {0}',
    cursorHere: 'The cursor is on this computer.',
    cursorOn: 'The cursor is on “{0}”.',
    connectedCount: 'Computers connected: {0} of {1}.',
    noOthers: 'There are no other screens in the layout yet — add them below.',
    clientConnected: 'Connected to {0}. The cursor is on another computer.',
    clientActive: 'Connected to {0}. The cursor is here.',
    clientWaiting: "Waiting for the server {0}…",
    errUnreachable: "The server {0} does not answer. Check the address, that edgehop is running there, and that the server's firewall allows its port.",
    errBadKey: "The server answers, but the key does not match. Copy the key from the edgehop page on the server.",
    errNotInLayout: "The server answers, but it does not know the screen “{0}” yet. Open the edgehop page on the computer with the mouse: this computer shows up at the top there with “Add on the left” and “Add on the right” buttons.",
    errSameName: "The name “{0}” is already used by the server itself. Give this computer a different name.",
    errVersion: "The server runs a different edgehop version. Update edgehop on both computers.",
    clientNote: "Where each screen stands (left, right, above, below) is set on the computer with the mouse. This computer must be called “{0}” there.",
    pendingText: "“{0}” ({1}) wants to connect, but it is not in the layout yet. Where does it stand relative to this computer?",
    addLeft: "Add on the left", addRight: "Add on the right",
    leftCap: "← left", rightCap: "right →",
    above: "Screen above this computer", below: "Screen below this computer",
    aboveBelowHint: "Optional: the name of a computer whose screen stands above or below.",
    hotkeyNote: '{0} brings the cursor back to this computer.',
    thisComputer: 'this computer', online: 'connected', offlineState: 'offline', cursor: 'cursor',
    setupTitle: 'Settings',
    roleServer: 'The mouse and keyboard are connected here',
    roleServerText: 'This computer shares them with the others (server).',
    roleClient: 'This computer is controlled from another',
    roleClientText: 'The mouse and keyboard are connected to another computer (client).',
    name: 'Name of this screen',
    nameHint: 'A short name without spaces; different on every computer.',
    serverAddr: 'Server address',
    serverAddrHint: 'IP address of the computer with the mouse — it is shown on its edgehop page.',
    key: 'Key',
    keyHintServer: 'The shared secret. The same key must be entered on every computer.',
    keyHintClient: 'Copy the key from the edgehop page on the server.',
    show: 'Show', hide: 'Hide', copy: 'Copy', copied: 'Copied', generate: 'New key',
    layout: 'Screens from left to right',
    layoutHint: 'Arrange the screens the way they stand on the desk. The names must match the names set on those computers.',
    addScreen: 'Add screen', addPlaceholder: 'name, e.g. macbook',
    moveLeft: 'Move left', moveRight: 'Move right', remove: 'Remove',
    screenOpts: 'Screen options',
    clipShare: "Shared clipboard: copied text and files can be pasted on the other computer",
    clipTitle: "Clipboard: limits",
    clipMax: "Largest size to transfer, MB",
    clipHint: "Larger content is not transferred. Images (screenshots, for example) are not transferred yet.",
    clipLast: "Last transfer: {0}",
    sendTitle: "Send files",
    dropText: "Drop files or folders here — they appear on the other computer.",
    pickFiles: "Choose files", pickFolder: "Choose a folder", cancel: "Cancel",
    sendTo: "To:",
    sendNoTarget: "No connected computer accepts files right now: it needs edgehop 0.3 or newer.",
    sendCopying: "Preparing: file {0} of {1}, {2} in total",
    sendSending: "Sending to “{0}”…",
    sendDone: "Sent to “{0}”: {1} file(s), {2}.",
    sendFailed: "Could not send: {0}",
    sendEmpty: "There are no files here that can be sent.",
    sendReadError: "the file could not be read",
    sendStopped: "edgehop was stopped",
    savedIn: "Files sent to this computer are saved in {0}",
    rwinServer: "Remote windows (experimental): show windows of the other computers here, in windows of their own",
    rwinClient: "Remote windows (experimental): let the computer with the mouse show this computer's windows and control them",
    rwinHint: "Turn it on on both computers. Windows of Windows and macOS computers can be shown.",
    rwinTitle: "Remote windows (experimental)",
    rwinOpen: "Choose a window",
    rwinText: "A window of another computer opens here in a window of its own; the mouse and keyboard in it work on that computer. Allowed by: {0}.",
    rwinNone: "No connected computer has allowed showing its windows yet: turn on “Remote windows” in the edgehop settings there and save.",
    rwinAllowed: "The computer with the mouse may show this computer's windows and control them.",
    speedHint: "The cursor speed is matched to the size of each screen automatically. The number below multiplies it: 1 keeps it, 1.5 makes it one and a half times faster.",
    speed: 'Cursor speed',
    swap: 'Swap Alt and Win/Cmd (PC keyboard ↔ Mac)',
    advanced: 'Advanced',
    port: 'Port',
    portHint: 'TCP and UDP. It must be allowed in this computer\'s firewall.',
    hotkey: 'Release hotkey',
    hotkeyHint: 'Brings the cursor back to this computer. For example ctrl+alt+shift+backspace; none turns it off.',
    hideCursor: 'Hide this computer\'s cursor while it controls another one',
    sizeTitle: 'Screen size',
    screenSize: 'Desktop size',
    screenSizeHint: 'Leave empty to detect it automatically. Only needed when the size is detected incorrectly (mostly on Linux).',
    saveStart: 'Save and start', save: 'Save only', saved: 'Saved',
    saveFailed: 'Could not save: {0}',
    actionFailed: 'Failed: {0}',
    errRole: 'Choose this computer\'s role.',
    errName: 'Enter a name for this screen.',
    errSpace: 'A screen name cannot contain spaces.',
    errKey: 'Enter the key.',
    errServer: 'Enter the server address.',
    errDup: 'The screen “{0}” is already in the layout.',
    configError: 'The settings file contains an error: {0}\nSave the settings again to fix it.',
    othersTitle: 'How to connect the other computers',
    othersStep1: 'Download edgehop on the other computer and start it.',
    download: 'Download',
    othersStep2: 'Choose “This computer is controlled from another” there and enter:',
    othersStep3: 'Or do the same with one command:',
    addFirst: 'add a screen to the layout above first',
    thisTitle: 'This computer',
    factName: 'Computer name', factSystem: 'System', factDesktop: 'Desktop',
    factMonitors: 'monitors: {0}', factAddr: 'Addresses', factConfig: 'Settings file',
    logTitle: 'Log',
  },
};

function pickLanguage() {
  try {
    const stored = localStorage.getItem('edgehop-lang');
    if (stored === 'uk' || stored === 'en') return stored;
  } catch (e) { /* storage may be unavailable */ }
  const nav = (navigator.language || 'en').toLowerCase();
  return nav.startsWith('uk') ? 'uk' : 'en';
}

let lang = pickLanguage();

function t(key, ...args) {
  let s = TEXT[lang][key];
  if (s === undefined) s = TEXT.en[key] !== undefined ? TEXT.en[key] : key;
  return s.replace(/\{(\d)\}/g, (m, i) => (args[i] !== undefined ? args[i] : ''));
}

// ---------------------------------------------------------------------------
// Small helpers

const $ = (id) => document.getElementById(id);
const clone = (v) => JSON.parse(JSON.stringify(v));
const show = (id, on) => { $(id).hidden = !on; };

function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else if (v === true) node.setAttribute(k, '');
    else if (v !== false && v !== null && v !== undefined) node.setAttribute(k, v);
  }
  for (const c of children) if (c) node.append(c);
  return node;
}

async function api(path, body) {
  const opt = { credentials: 'same-origin', cache: 'no-store' };
  if (body !== undefined) {
    opt.method = 'POST';
    opt.headers = { 'Content-Type': 'application/json' };
    opt.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opt);
  let data = null;
  try { data = await resp.json(); } catch (e) { /* not JSON */ }
  if (!resp.ok) {
    const err = new Error((data && data.error) || ('HTTP ' + resp.status));
    err.status = resp.status;
    throw err;
  }
  return data;
}

async function copyText(text, button) {
  try {
    await navigator.clipboard.writeText(text);
  } catch (e) {
    const area = el('textarea', {});
    area.value = text;
    document.body.append(area);
    area.select();
    document.execCommand('copy');
    area.remove();
  }
  if (button) {
    const old = button.textContent;
    button.textContent = t('copied');
    setTimeout(() => { button.textContent = old; }, 1200);
  }
}

// ---------------------------------------------------------------------------
// State

let S = null;        // last state reported by edgehop
let form = null;     // the settings being edited
let showKey = false;
let lastLive = '';   // what the live parts were last rendered from
let lastLog = '';
let busy = false;

function selfName() { return (form.name || '').trim(); }

function otherScreens(cfg) {
  return (cfg.row || []).filter((n) => n !== cfg.name);
}

function ensureScreen(name) {
  if (!form.screens) form.screens = {};
  if (!form.screens[name]) form.screens[name] = { scale: 1, swapAltMeta: false };
  return form.screens[name];
}

// ---------------------------------------------------------------------------
// Settings form

function renderForm() {
  const role = form.role;
  $('role-server').setAttribute('aria-checked', role === 'server');
  $('role-client').setAttribute('aria-checked', role === 'client');
  show('fields', role === 'server' || role === 'client');
  show('server-fields', role === 'server');
  show('client-fields', role === 'client');
  if (!role) return;

  $('f-name').value = form.name || '';
  $('f-key').value = form.key || '';
  $('f-key').type = showKey ? 'text' : 'password';
  $('key-show').textContent = t(showKey ? 'hide' : 'show');
  show('key-new', role === 'server');
  $('key-hint').textContent = t(role === 'server' ? 'keyHintServer' : 'keyHintClient');
  $('f-server').value = form.server || '';
  $('f-port').value = form.port || S.defaultPort;
  $('f-hotkey').value = form.releaseHotkey || '';
  $('f-hide').checked = !!form.hideCursor;
  $('f-size').value = form.screenSize || '';
  $('f-size').placeholder = S.desktopW && S.desktopH ? S.desktopW + 'x' + S.desktopH : '1920x1080';
  $('row-add').placeholder = t('addPlaceholder');
  $('f-clip').checked = !!form.clipboard;
  $('f-clipmax').value = form.clipboardMaxMB || 200;
  $('f-rwin').checked = !!form.remoteWindows;
  $('rwin-label').textContent = t(role === 'server' ? 'rwinServer' : 'rwinClient');
  $('f-above').value = form.above || '';
  $('f-below').value = form.below || '';
  $('client-note').textContent = role === 'client' && selfName() ? t('clientNote', selfName()) : '';
  if (role === 'server') renderRow();
}

function renderRow() {
  const self = selfName();
  if (!form.row) form.row = [];
  if (self && !form.row.includes(self)) form.row.push(self);
  const row = $('row');
  row.replaceChildren();
  row.append(el('span', { class: 'row-cap', text: t('leftCap') }));
  form.row.forEach((name, i) => {
    const isSelf = name === self;
    const chip = el('div', { class: 'chip' + (isSelf ? ' self' : '') },
      el('span', { class: 'c-name', text: name }),
      isSelf ? el('span', { class: 'c-self', text: '(' + t('thisComputer') + ')' }) : null,
      el('button', { type: 'button', title: t('moveLeft'), 'aria-label': t('moveLeft'), disabled: i === 0, text: '←', onclick: () => moveScreen(i, -1) }),
      el('button', { type: 'button', title: t('moveRight'), 'aria-label': t('moveRight'), disabled: i === form.row.length - 1, text: '→', onclick: () => moveScreen(i, 1) }),
      isSelf ? null : el('button', { type: 'button', title: t('remove'), 'aria-label': t('remove'), text: '×', onclick: () => removeScreen(i) }));
    row.append(chip);
  });
  row.append(el('span', { class: 'row-cap', text: t('rightCap') }));

  const others = form.row.filter((n) => n !== self);
  for (const extra of [form.above, form.below]) {
    const name = (extra || '').trim();
    if (name && name !== self && !others.includes(name)) others.push(name);
  }
  show('screen-opts-wrap', others.length > 0);
  const opts = $('screen-opts');
  opts.replaceChildren();
  for (const name of others) {
    const o = ensureScreen(name);
    const speed = el('input', { type: 'number', min: '0.2', max: '5', step: '0.05', value: String(o.scale || 1) });
    speed.addEventListener('input', () => {
      const v = parseFloat(speed.value);
      if (v > 0) ensureScreen(name).scale = v;
    });
    const swap = el('input', { type: 'checkbox' });
    swap.checked = !!o.swapAltMeta;
    swap.addEventListener('change', () => { ensureScreen(name).swapAltMeta = swap.checked; });
    opts.append(el('div', { class: 'opt-row' },
      el('span', { class: 'o-name', text: name }),
      el('label', {}, el('span', { text: t('speed') }), speed),
      el('label', {}, swap, el('span', { text: t('swap') }))));
  }
}

function moveScreen(i, delta) {
  const j = i + delta;
  if (j < 0 || j >= form.row.length) return;
  [form.row[i], form.row[j]] = [form.row[j], form.row[i]];
  renderRow();
}

function removeScreen(i) {
  const [name] = form.row.splice(i, 1);
  if (form.screens) delete form.screens[name];
  renderRow();
}

function addScreen() {
  const input = $('row-add');
  const name = input.value.trim();
  if (!name) return;
  if (/\s/.test(name)) return formError(t('errSpace'));
  if (form.row.includes(name)) return formError(t('errDup', name));
  formError('');
  form.row.push(name);
  input.value = '';
  renderRow();
  input.focus();
}

function formError(msg) {
  $('form-error').textContent = msg;
  show('form-error', !!msg);
}

async function chooseRole(role) {
  form.role = role;
  if (!form.name) form.name = S.hostname;
  if (role === 'server') {
    if (!form.row || form.row.length === 0) form.row = [selfName()];
    if (!form.key) {
      try { form.key = (await api('/api/newkey', {})).key; } catch (e) { /* the user can type one */ }
    }
  }
  formError('');
  renderForm();
}

function validate() {
  if (form.role !== 'server' && form.role !== 'client') return t('errRole');
  if (!selfName()) return t('errName');
  if (/\s/.test(selfName())) return t('errSpace');
  if (!(form.key || '').trim()) return t('errKey');
  if (form.role === 'client' && !(form.server || '').trim()) return t('errServer');
  return '';
}

async function save(start) {
  const problem = validate();
  if (problem) return formError(problem);
  formError('');
  const cfg = clone(form);
  cfg.name = selfName();
  cfg.port = parseInt(cfg.port, 10) || S.defaultPort;
  cfg.clipboardMaxMB = parseInt(cfg.clipboardMaxMB, 10) || 200;
  if (cfg.role === 'client') { cfg.row = []; cfg.screens = {}; }
  busy = true;
  setButtons();
  try {
    S = await api('/api/config', { config: cfg, start: start });
    form = clone(S.config);
    renderForm();
    renderLive(true);
    show('saved', true);
    setTimeout(() => show('saved', false), 2000);
  } catch (e) {
    formError(t('saveFailed', e.message));
  } finally {
    busy = false;
    setButtons();
  }
}

// ---------------------------------------------------------------------------
// Live status

function setButtons() {
  if (!S) return;
  const running = S.engine.running;
  $('start').textContent = t(running ? 'restart' : 'start');
  $('start').disabled = busy || !S.configExists || !!S.configError;
  $('stop').disabled = busy || !running;
  $('save').disabled = busy;
  $('save-start').disabled = busy;
}

function screenBox(s, focusName, running) {
  const focused = running && ((s.self && focusName === '') || (!s.self && focusName === s.name));
  let cls = 'screen';
  if (s.self) cls += ' self';
  else if (s.connected) cls += ' connected';
  if (focused) cls += ' focus';
  let info = '';
  let state = '';
  if (s.self) {
    state = t('thisComputer');
    if (S.desktopW) info = S.desktopW + '×' + S.desktopH;
  } else if (s.connected) {
    state = t('online');
    info = [s.os, s.w + '×' + s.h, s.udp ? 'UDP' : 'TCP', s.rttMs > 0 ? s.rttMs + ' ms' : ''].filter(Boolean).join(' · ');
  } else if (running) {
    state = t('offlineState');
  }
  return el('div', { class: cls },
    focused ? el('span', { class: 'cursor', text: '● ' + t('cursor') }) : null,
    el('div', { class: 's-name', text: s.name }),
    el('div', { class: 's-info', text: info }),
    el('div', { class: 's-state', text: state }));
}

// Why a client is not connected, in words the user can act on.
function clientProblem(st) {
  const e = st.error || '';
  if (e.includes('not in the server')) return t('errNotInLayout', st.name);
  if (e.includes('does not match')) return t('errBadKey');
  if (e.includes('already used by the server')) return t('errSameName', st.name);
  if (e.includes('protocol version') || e.includes('incompatible edgehop version')) return t('errVersion');
  if (e.includes('cannot reach')) return t('errUnreachable', st.server);
  return t('clientWaiting', st.server);
}

// addPending puts a waiting computer at one end of the row and restarts.
function addPending(name, atLeft) {
  if (!form.row || form.row.length === 0) form.row = [selfName()];
  if (!form.row.includes(name)) {
    if (atLeft) form.row.unshift(name);
    else form.row.push(name);
  }
  renderForm();
  save(true);
}

function renderPending(st) {
  const box = $('pending');
  box.replaceChildren();
  if (!st || st.role !== 'server' || !form || form.role !== 'server') return;
  for (const p of st.pending || []) {
    const where = [p.os, p.addr].filter(Boolean).join(', ');
    box.append(el('div', { class: 'pending-item' },
      el('span', { class: 'p-text', text: t('pendingText', p.name, where) }),
      el('button', { type: 'button', class: 'small primary', text: t('addLeft'), onclick: () => addPending(p.name, true) }),
      el('button', { type: 'button', class: 'small primary', text: t('addRight'), onclick: () => addPending(p.name, false) })));
  }
}

function renderLive(force) {
  const eng = S.engine;
  const st = eng.status || null;
  const key = JSON.stringify([lang, eng, S.config, S.configExists, S.configError, S.addresses, S.desktopW, S.desktopH, S.hostname, S.version, showKey]);
  if (!force && key === lastLive) {
    if (sending.phase === 'sending') { checkSend(); renderSend(); }
    renderLog();
    return;
  }
  lastLive = key;

  $('version').textContent = S.version ? 'v' + S.version.replace(/^v/, '') : '';
  setButtons();

  const pill = $('pill');
  const line = $('status-line');
  const screens = $('screens');
  const hint = $('status-hint');
  screens.replaceChildren();
  hint.textContent = '';
  pill.className = 'pill';
  renderPending(eng.running ? st : null);

  $('engine-exit').textContent = eng.exit ? t('stoppedBecause', eng.exit) : '';
  show('engine-exit', !eng.running && !!eng.exit);
  $('config-error').textContent = S.configError ? t('configError', S.configError) : '';
  show('config-error', !!S.configError);

  if (!eng.running) {
    pill.textContent = t('pillStopped');
    line.textContent = S.configExists && !S.configError ? t('stoppedLine') : t('needSetup');
    if (S.configExists && S.config.role === 'server') {
      for (const name of S.config.row || []) {
        screens.append(screenBox({ name: name, self: name === S.config.name }, '', false));
      }
    }
  } else if (!st) {
    pill.textContent = t('pillWaiting');
    pill.classList.add('wait');
    line.textContent = t('starting');
  } else if (st.role === 'server') {
    const others = (st.screens || []).filter((s) => !s.self);
    const connected = others.filter((s) => s.connected).length;
    pill.textContent = t(connected > 0 ? 'pillRunning' : 'pillWaiting');
    pill.classList.add(connected > 0 ? 'on' : 'wait');
    const where = st.focus ? t('cursorOn', st.focus) : t('cursorHere');
    line.textContent = others.length === 0 ? t('noOthers') : where + ' ' + t('connectedCount', connected, others.length);
    for (const s of st.screens || []) screens.append(screenBox(s, st.focus || '', true));
    if (st.hotkey) hint.textContent = t('hotkeyNote', st.hotkey);
    if (st.clipboard) hint.textContent += (hint.textContent ? ' ' : '') + t('clipLast', st.clipboard);
  } else {
    pill.textContent = t(st.connected ? 'pillRunning' : 'pillWaiting');
    pill.classList.add(st.connected ? 'on' : 'wait');
    line.textContent = !st.connected ? clientProblem(st)
      : st.active ? t('clientActive', st.server) : t('clientConnected', st.server);
    if (st.clipboard) hint.textContent = t('clipLast', st.clipboard);
    if (st.view) hint.textContent += (hint.textContent ? ' ' : '') + t('rwinAllowed');
  }

  checkSend();
  renderSend();
  renderRemoteWindows();
  renderOthers();
  renderThis();
  renderLog();
}

function renderOthers() {
  const cfg = S.config;
  const ok = S.configExists && !S.configError && cfg.role === 'server';
  show('others-card', ok);
  if (!ok) return;
  const suffix = cfg.port && cfg.port !== S.defaultPort ? ':' + cfg.port : '';
  const addrs = (S.addresses || []).map((a) => a + suffix);
  const addrBox = $('o-addr');
  addrBox.replaceChildren();
  addrs.forEach((a, i) => {
    if (i > 0) addrBox.append(', ');
    addrBox.append(el('code', { text: a }));
  });
  $('o-key').textContent = showKey ? cfg.key : '•'.repeat(12);
  const names = otherScreens(cfg);
  const nameBox = $('o-names');
  nameBox.replaceChildren();
  if (names.length === 0) nameBox.textContent = t('addFirst');
  names.forEach((n, i) => {
    if (i > 0) nameBox.append(', ');
    nameBox.append(el('code', { text: n }));
  });
  const cmd = 'edgehop init client --server ' + (addrs[0] || 'ADDRESS') + ' --key ' + cfg.key + ' --name ' + (names[0] || 'NAME');
  $('o-cmd').textContent = showKey ? cmd : cmd.replace(cfg.key, '•'.repeat(12));
  $('o-cmd').dataset.full = cmd;
  const link = $('download-link');
  show('download-link', !!S.repoUrl);
  if (S.repoUrl) link.href = S.repoUrl.replace(/\/$/, '') + '/releases/latest';
}

function renderThis() {
  const box = $('this');
  box.replaceChildren();
  const add = (label, value) => box.append(el('dt', { text: label }), el('dd', { text: value }));
  add(t('factName'), S.hostname);
  add(t('factSystem'), S.os + ' / ' + S.arch);
  let desk = S.desktopW ? S.desktopW + '×' + S.desktopH : '—';
  if ((S.monitors || []).length > 1) desk += ' (' + t('factMonitors', S.monitors.length) + ')';
  add(t('factDesktop'), desk);
  add(t('factAddr'), (S.addresses || []).join(', ') || '—');
  add(t('factConfig'), S.configPath);
}

function renderLog() {
  const text = (S.logs || []).join('\n');
  if (text === lastLog) return;
  lastLog = text;
  const log = $('log');
  const atBottom = log.scrollTop + log.clientHeight >= log.scrollHeight - 8;
  log.textContent = text;
  if (atBottom) log.scrollTop = log.scrollHeight;
}

// ---------------------------------------------------------------------------
// Remote windows (experimental): on the server, a card that opens the list
// of windows of the other computers.

function renderRemoteWindows() {
  const st = S && S.engine.running ? (S.engine.status || null) : null;
  const on = !!(st && st.role === 'server' && st.view && st.view.url);
  show('rwin-card', on);
  if (!on) return;
  const names = (st.screens || []).filter((s) => !s.self && s.connected && s.view).map((s) => s.name);
  $('rwin-open').disabled = names.length === 0;
  $('rwin-text').textContent = names.length === 0 ? t('rwinNone') : t('rwinText', names.join(', '));
}

// ---------------------------------------------------------------------------
// Sending files
//
// A page is never told where a dropped file lives on disk, so the file is
// handed to edgehop on this computer first (a local copy), and edgehop sends
// it on to the other computer.

const sending = {
  phase: 'idle',   // idle | copying | sending
  to: '',          // the chosen receiver
  target: '',      // the receiver of the transfer in progress
  batch: '',
  xhr: null,
  cancelled: false,
  frac: 0,
  text: '',
  error: '',
  done: '',
  count: 0,
  bytes: 0,
  waitFor: 0,      // the engine's sendDone value that means "finished"
};

function formatSize(n) {
  const units = lang === 'uk' ? ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'] : ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  const digits = i === 0 || n >= 100 ? 0 : 1;
  return n.toFixed(digits).replace('.', lang === 'uk' ? ',' : '.') + ' ' + units[i];
}

// Who can receive files right now.
function sendTargets(st) {
  if (!S || !S.engine.running || !st) return [];
  if (st.role === 'server') {
    return (st.screens || []).filter((s) => !s.self && s.connected && s.files).map((s) => s.name);
  }
  return st.connected && st.files ? [st.serverName || st.server] : [];
}

function renderSend() {
  const st = S && S.engine.running ? (S.engine.status || null) : null;
  show('send-card', !!st);
  if (!st) return;

  const targets = sendTargets(st);
  if (!targets.includes(sending.to)) sending.to = targets[0] || '';
  const idle = sending.phase === 'idle';
  const box = $('send-targets');
  box.replaceChildren();
  box.removeAttribute('role');
  if (targets.length === 1) {
    box.append(el('span', { class: 't-label', text: t('sendTo') }), el('span', { class: 't-one', text: targets[0] }));
  } else if (targets.length > 1) {
    box.setAttribute('role', 'radiogroup');
    box.append(el('span', { class: 't-label', text: t('sendTo') }));
    for (const name of targets) {
      box.append(el('button', {
        type: 'button', class: 'small', role: 'radio', 'aria-checked': String(name === sending.to), disabled: !idle, text: name,
        onclick: () => { sending.to = name; renderSend(); },
      }));
    }
  }

  const ready = targets.length > 0 && idle;
  $('drop').classList.toggle('off', !ready);
  $('pick-files').disabled = !ready;
  $('pick-folder').disabled = !ready;

  show('send-progress', !idle);
  if (sending.phase === 'sending') $('send-bar').removeAttribute('value'); // no percentage for this part
  else $('send-bar').value = Math.round(sending.frac * 1000);
  $('send-text').textContent = sending.text;
  show('send-cancel', sending.phase === 'copying');
  $('send-error').textContent = sending.error;
  show('send-error', !!sending.error);
  $('send-done').textContent = sending.done;
  show('send-done', !!sending.done);

  const hint = [];
  if (targets.length === 0) hint.push(t('sendNoTarget'));
  if (st.downloads) hint.push(t('savedIn', st.downloads));
  $('send-hint').textContent = hint.join(' ');
}

// checkSend notices that the engine finished (or gave up on) the transfer.
function checkSend() {
  if (sending.phase !== 'sending') return;
  const st = S.engine.running ? (S.engine.status || null) : null;
  if (!st) {
    sending.phase = 'idle';
    sending.error = t('sendFailed', t('sendStopped'));
    return;
  }
  if ((st.sendDone || 0) < sending.waitFor) return;
  sending.phase = 'idle';
  if (st.sendError) sending.error = t('sendFailed', st.sendError);
  else sending.done = t('sendDone', sending.target, sending.count, formatSize(sending.bytes));
}

function readAllEntries(reader) {
  // readEntries hands the folder over in portions; an empty one ends it.
  return new Promise((resolve, reject) => {
    const all = [];
    const next = () => reader.readEntries((part) => {
      if (part.length === 0) resolve(all);
      else { all.push(...part); next(); }
    }, reject);
    next();
  });
}

async function walkEntry(entry, prefix, out) {
  if (entry.isFile) {
    const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
    out.push({ file: file, path: prefix + entry.name });
  } else if (entry.isDirectory) {
    for (const child of await readAllEntries(entry.createReader())) {
      await walkEntry(child, prefix + entry.name + '/', out);
    }
  }
}

// droppedItems lists the files of a drop, folders included. The entries must
// be taken before the first await: the drop data is gone after that.
async function droppedItems(dt) {
  const out = [];
  const entries = [];
  for (const item of Array.from(dt.items || [])) {
    if (item.kind !== 'file') continue;
    const entry = item.webkitGetAsEntry ? item.webkitGetAsEntry() : null;
    if (entry) entries.push(entry);
    else {
      const file = item.getAsFile();
      if (file) out.push({ file: file, path: file.name });
    }
  }
  const plain = Array.from(dt.files || []);
  for (const entry of entries) await walkEntry(entry, '', out);
  if (out.length === 0 && entries.length === 0) for (const file of plain) out.push({ file: file, path: file.name });
  return out;
}

function pickedItems(input) {
  const out = Array.from(input.files || []).map((file) => ({ file: file, path: file.webkitRelativePath || file.name }));
  input.value = '';
  return out;
}

function uploadOne(batch, item, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    sending.xhr = xhr;
    xhr.open('POST', '/api/send/file?batch=' + encodeURIComponent(batch) + '&path=' + encodeURIComponent(item.path));
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.onprogress = (e) => onProgress(e.loaded);
    xhr.onload = () => {
      if (xhr.status === 200) { resolve(); return; }
      let msg = 'HTTP ' + xhr.status;
      try { msg = JSON.parse(xhr.responseText).error || msg; } catch (e) { /* not JSON */ }
      reject(new Error(item.path + ': ' + msg));
    };
    xhr.onerror = () => reject(new Error(item.path + ': ' + t('sendReadError')));
    xhr.onabort = () => reject(new Error('cancelled'));
    xhr.send(item.file);
  });
}

async function sendItems(items) {
  if (sending.phase !== 'idle' || !sending.to) return;
  sending.error = '';
  sending.done = '';
  if (items.length === 0) {
    sending.error = t('sendEmpty');
    renderSend();
    return;
  }
  const total = items.reduce((n, it) => n + it.file.size, 0);
  Object.assign(sending, {
    phase: 'copying', target: sending.to, batch: '', cancelled: false, frac: 0,
    count: items.length, bytes: total, text: t('sendCopying', 1, items.length, formatSize(total)),
  });
  renderSend();
  try {
    sending.batch = (await api('/api/send/begin', {})).batch;
    let before = 0;
    for (let i = 0; i < items.length; i++) {
      if (sending.cancelled) throw new Error('cancelled');
      sending.text = t('sendCopying', i + 1, items.length, formatSize(total));
      $('send-text').textContent = sending.text;
      await uploadOne(sending.batch, items[i], (loaded) => {
        sending.frac = total > 0 ? Math.min(1, (before + loaded) / total) : 1;
        $('send-bar').value = Math.round(sending.frac * 1000);
      });
      before += items[i].file.size;
    }
    const fresh = await api('/api/state');
    sending.waitFor = (((fresh.engine || {}).status || {}).sendDone || 0) + 1;
    await api('/api/send/commit', { batch: sending.batch, to: sending.target });
    sending.phase = 'sending';
    sending.text = t('sendSending', sending.target);
  } catch (e) {
    if (sending.batch) api('/api/send/cancel', { batch: sending.batch }).catch(() => {});
    sending.phase = 'idle';
    sending.error = sending.cancelled ? '' : t('sendFailed', e.message);
  }
  sending.xhr = null;
  checkSend(); // a small transfer may be over already
  renderSend();
}

function initSend() {
  const drop = $('drop');
  let depth = 0; // dragenter and dragleave also fire for every child element
  const hasFiles = (e) => e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files');
  // The whole window takes the drop, so a file that misses the frame by a
  // few pixels is sent too - instead of the browser opening it and leaving.
  window.addEventListener('dragenter', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    depth++;
    if (!$('send-card').hidden) drop.classList.add('over');
  });
  window.addEventListener('dragover', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    const ready = !$('send-card').hidden && sending.phase === 'idle' && !!sending.to;
    e.dataTransfer.dropEffect = ready ? 'copy' : 'none';
  });
  window.addEventListener('dragleave', (e) => {
    if (!hasFiles(e)) return;
    depth = Math.max(0, depth - 1);
    if (depth === 0) drop.classList.remove('over');
  });
  window.addEventListener('drop', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    depth = 0;
    drop.classList.remove('over');
    if ($('send-card').hidden || sending.phase !== 'idle' || !sending.to) return;
    droppedItems(e.dataTransfer).then(sendItems, (err) => {
      sending.error = t('sendFailed', err && err.message ? err.message : t('sendReadError'));
      renderSend();
    });
  });
  $('pick-files').addEventListener('click', () => $('file-input').click());
  $('pick-folder').addEventListener('click', () => $('folder-input').click());
  $('file-input').addEventListener('change', (e) => sendItems(pickedItems(e.currentTarget)));
  $('folder-input').addEventListener('change', (e) => sendItems(pickedItems(e.currentTarget)));
  $('send-cancel').addEventListener('click', () => {
    sending.cancelled = true;
    if (sending.xhr) sending.xhr.abort();
  });
}

// ---------------------------------------------------------------------------
// Wiring

function applyTexts() {
  document.documentElement.lang = lang;
  for (const node of document.querySelectorAll('[data-t]')) node.textContent = t(node.dataset.t);
  $('lang').textContent = t('langButton');
}

async function engineAction(path) {
  busy = true;
  setButtons();
  try {
    if (path === '/api/start' && S.engine.running) await api('/api/stop', {});
    S = await api(path, {});
    renderLive(true);
  } catch (e) {
    $('engine-exit').textContent = t('actionFailed', e.message);
    show('engine-exit', true);
  } finally {
    busy = false;
    setButtons();
  }
}

async function poll() {
  if (busy) return;
  try {
    S = await api('/api/state');
  } catch (e) {
    const locked = e.status === 401;
    show('locked', locked);
    show('offline', !locked);
    if (locked) show('app', false);
    $('pill').className = 'pill';
    $('pill').textContent = '';
    return;
  }
  show('locked', false);
  show('offline', false);
  show('app', true);
  if (!form) {
    form = clone(S.config);
    renderForm();
  }
  renderLive(false);
}

function init() {
  applyTexts();
  $('lang').addEventListener('click', () => {
    lang = lang === 'uk' ? 'en' : 'uk';
    try { localStorage.setItem('edgehop-lang', lang); } catch (e) { /* ignore */ }
    applyTexts();
    if (S) { if (form) renderForm(); renderLive(true); }
  });
  $('role-server').addEventListener('click', () => chooseRole('server'));
  $('role-client').addEventListener('click', () => chooseRole('client'));

  $('f-name').addEventListener('input', () => {
    const old = selfName();
    form.name = $('f-name').value;
    const now = selfName();
    $('client-note').textContent = form.role === 'client' && now ? t('clientNote', now) : '';
    if (form.role === 'server') {
      form.row = (form.row || []).map((n) => (n === old ? now : n)).filter((n) => n !== '');
      renderRow();
    }
  });
  $('f-key').addEventListener('input', () => { form.key = $('f-key').value; });
  $('f-above').addEventListener('change', () => { form.above = $('f-above').value.trim(); renderRow(); });
  $('f-below').addEventListener('change', () => { form.below = $('f-below').value.trim(); renderRow(); });
  $('f-server').addEventListener('input', () => { form.server = $('f-server').value; });
  $('f-port').addEventListener('input', () => { form.port = parseInt($('f-port').value, 10) || 0; });
  $('f-hotkey').addEventListener('input', () => { form.releaseHotkey = $('f-hotkey').value; });
  $('f-hide').addEventListener('change', () => { form.hideCursor = $('f-hide').checked; });
  $('f-size').addEventListener('input', () => { form.screenSize = $('f-size').value; });
  $('f-clip').addEventListener('change', () => { form.clipboard = $('f-clip').checked; });
  $('f-rwin').addEventListener('change', () => { form.remoteWindows = $('f-rwin').checked; });
  $('rwin-open').addEventListener('click', () => {
    const view = S && S.engine.running && S.engine.status ? S.engine.status.view : null;
    if (view && view.url) window.open(view.url, 'edgehop-windows', 'popup=yes,width=560,height=640');
  });
  $('f-clipmax').addEventListener('input', () => { form.clipboardMaxMB = parseInt($('f-clipmax').value, 10) || 0; });

  $('key-show').addEventListener('click', () => {
    showKey = !showKey;
    renderForm();
    if (S) renderLive(true);
  });
  $('key-copy').addEventListener('click', (e) => copyText(form.key || '', e.currentTarget));
  $('key-new').addEventListener('click', async () => {
    try {
      form.key = (await api('/api/newkey', {})).key;
      renderForm();
    } catch (e) { formError(t('actionFailed', e.message)); }
  });
  $('row-add-btn').addEventListener('click', addScreen);
  $('row-add').addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); addScreen(); } });

  $('save-start').addEventListener('click', () => save(true));
  $('save').addEventListener('click', () => save(false));
  $('start').addEventListener('click', () => engineAction('/api/start'));
  $('stop').addEventListener('click', () => engineAction('/api/stop'));
  $('o-key-copy').addEventListener('click', (e) => copyText(S.config.key || '', e.currentTarget));
  $('o-cmd-copy').addEventListener('click', (e) => copyText($('o-cmd').dataset.full || '', e.currentTarget));
  initSend();

  poll();
  setInterval(poll, 1000);
}

init();
