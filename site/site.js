'use strict';

// Where the project lives. The deploy workflow writes "owner/repo" into the
// meta tag; when the page is opened straight from a checkout, or served from
// <owner>.github.io/<repo>/, it is worked out from the address instead.
function findRepo() {
  const meta = document.querySelector('meta[name="edgehop-repo"]');
  const value = meta ? meta.content : '';
  if (value && value.indexOf('/') > 0 && value.indexOf('__') !== 0) return value;
  const host = location.hostname;
  const first = location.pathname.split('/').filter(Boolean)[0];
  if (host.endsWith('.github.io') && first) return host.replace('.github.io', '') + '/' + first;
  return '';
}

const repo = findRepo();
const repoUrl = repo ? 'https://github.com/' + repo : '';

function setLinks() {
  const link = (selector, suffix) => {
    for (const a of document.querySelectorAll(selector)) {
      if (repoUrl) a.href = repoUrl + suffix;
      else a.removeAttribute('href');
    }
  };
  link('.repo-link', '');
  link('.readme-link', '#readme');
  link('.issues-link', '/issues');
  link('.releases-link', '/releases');
  for (const a of document.querySelectorAll('a[data-file]')) {
    if (repoUrl) a.href = repoUrl + '/releases/latest/download/' + a.dataset.file;
    else a.removeAttribute('href');
  }
}

// Offer the file for the visitor's system on the main button.
function detectSystem() {
  const ua = navigator.userAgent || '';
  const platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '';
  const s = (platform + ' ' + ua).toLowerCase();
  if (s.includes('android') || s.includes('iphone') || s.includes('ipad')) return null;
  if (s.includes('win')) return { name: 'Windows', file: 'edgehop-windows-amd64.exe' };
  if (s.includes('mac')) return { name: 'macOS', file: 'edgehop-darwin-arm64' };
  if (s.includes('linux') || s.includes('x11')) return { name: 'Linux', file: 'edgehop-linux-amd64' };
  return null;
}

function setMainDownload() {
  const sys = detectSystem();
  const button = document.getElementById('main-download');
  if (!sys || !repoUrl) return; // the button keeps pointing at the download section
  document.getElementById('main-os').textContent = sys.name;
  button.href = repoUrl + '/releases/latest/download/' + sys.file;
}

// Languages: the page is written in English; Ukrainian texts sit in data-uk.
let lang = 'en';

function applyLanguage() {
  document.documentElement.lang = lang;
  for (const node of document.querySelectorAll('[data-uk]')) {
    if (node.dataset.en === undefined) node.dataset.en = node.textContent;
    node.textContent = lang === 'uk' ? node.dataset.uk : node.dataset.en;
  }
  document.getElementById('lang').textContent = lang === 'uk' ? 'EN' : 'УКР';
  showRelease();
}

let release = null;

function showRelease() {
  const info = document.getElementById('release-info');
  if (!release) { info.textContent = ''; return; }
  const date = new Date(release.published_at);
  const when = isNaN(date) ? '' : date.toLocaleDateString(lang === 'uk' ? 'uk-UA' : 'en-GB', { year: 'numeric', month: 'long', day: 'numeric' });
  info.textContent = (lang === 'uk' ? 'Остання версія: ' : 'Latest version: ') + release.tag_name + (when ? ' · ' + when : '');
}

async function loadRelease() {
  if (!repo) return;
  try {
    const resp = await fetch('https://api.github.com/repos/' + repo + '/releases/latest', { headers: { Accept: 'application/vnd.github+json' } });
    if (!resp.ok) return;
    release = await resp.json();
    showRelease();
  } catch (e) { /* the download links work without it */ }
}

function init() {
  try {
    const stored = localStorage.getItem('edgehop-lang');
    if (stored === 'uk' || stored === 'en') lang = stored;
    else if ((navigator.language || '').toLowerCase().startsWith('uk')) lang = 'uk';
  } catch (e) { /* storage may be unavailable */ }
  setLinks();
  setMainDownload();
  applyLanguage();
  document.getElementById('lang').addEventListener('click', () => {
    lang = lang === 'uk' ? 'en' : 'uk';
    try { localStorage.setItem('edgehop-lang', lang); } catch (e) { /* ignore */ }
    applyLanguage();
  });
  loadRelease();
}

init();
