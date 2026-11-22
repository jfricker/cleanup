// Renders a tmux `capture-pane -e -p` dump (ANSI SGR, truecolor or 256-colour) to a
// PNG on a terminal-like background, using the pre-installed Playwright Chromium.
//
//   node scripts/ansi2png.mjs IN.ansi OUT.png [cols] [light]
import { readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { chromium } = require('playwright');

const [, , inPath, outPath, colsArg = '132', theme = 'dark'] = process.argv;
const cols = Number(colsArg);
const dark = theme !== 'light';
const bg = dark ? '#1e1e1e' : '#ffffff';
const fg = dark ? '#d4d4d4' : '#1f1f1f';

const base16 = ['#000000', '#cd3131', '#0dbc79', '#e5e510', '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5',
  '#666666', '#f14c4c', '#23d18b', '#f5f543', '#3b8eea', '#d670d6', '#29b8db', '#ffffff'];
function xterm256(n) {
  if (n < 16) return base16[n];
  if (n < 232) {
    n -= 16;
    const v = [0, 95, 135, 175, 215, 255];
    return rgb(v[Math.floor(n / 36)], v[Math.floor(n / 6) % 6], v[n % 6]);
  }
  const g = 8 + (n - 232) * 10;
  return rgb(g, g, g);
}
function rgb(r, g, b) { return '#' + [r, g, b].map(x => x.toString(16).padStart(2, '0')).join(''); }
const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

const ansi = readFileSync(inPath, 'utf8').replace(/\n+$/, '');
let html = '';
let st = {};
let last = 0;
const re = /\x1b\[([0-9;]*)m/g;
const span = () => {
  const css = [];
  let c = st.color, b = st.background;
  if (st.reverse) [c, b] = [b || bg, c || fg];
  if (c) css.push('color:' + c);
  if (b) css.push('background:' + b);
  if (st.bold) css.push('font-weight:bold');
  if (st.italic) css.push('font-style:italic');
  if (st.underline) css.push('text-decoration:underline');
  if (st.faint) css.push('opacity:.6');
  return css.length ? `<span style="${css.join(';')}">` : '<span>';
};
html += span();
for (let m; (m = re.exec(ansi));) {
  html += esc(ansi.slice(last, m.index));
  last = re.lastIndex;
  const codes = m[1] === '' ? [0] : m[1].split(';').map(Number);
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    if (c === 0) st = {};
    else if (c === 1) st.bold = true;
    else if (c === 2) st.faint = true;
    else if (c === 3) st.italic = true;
    else if (c === 4) st.underline = true;
    else if (c === 7) st.reverse = true;
    else if (c === 22) { delete st.bold; delete st.faint; }
    else if (c === 23) delete st.italic;
    else if (c === 24) delete st.underline;
    else if (c === 27) delete st.reverse;
    else if (c === 39) delete st.color;
    else if (c === 49) delete st.background;
    else if (c >= 30 && c <= 37) st.color = base16[c - 30];
    else if (c >= 90 && c <= 97) st.color = base16[c - 90 + 8];
    else if (c >= 40 && c <= 47) st.background = base16[c - 40];
    else if (c >= 100 && c <= 107) st.background = base16[c - 100 + 8];
    else if (c === 38 || c === 48) {
      const key = c === 38 ? 'color' : 'background';
      if (codes[i + 1] === 2) { st[key] = rgb(codes[i + 2], codes[i + 3], codes[i + 4]); i += 4; }
      else if (codes[i + 1] === 5) { st[key] = xterm256(codes[i + 2]); i += 2; }
    }
  }
  html += '</span>' + span();
}
html += esc(ansi.slice(last)) + '</span>';

const page = `<!doctype html><meta charset=utf-8><body style="margin:0;background:${bg}">
<pre id=t style="display:inline-block;margin:0;padding:14px 16px;font:14px/1.3 'DejaVu Sans Mono',Menlo,monospace;color:${fg};background:${bg};white-space:pre">${html}</pre>`;
const browser = await chromium.launch();
const p = await browser.newPage({ deviceScaleFactor: 2, viewport: { width: cols * 9 + 60, height: 400 } });
await p.setContent(page);
await (await p.$('#t')).screenshot({ path: outPath });
await browser.close();
if (process.env.ANSI2PNG_HTML) writeFileSync(process.env.ANSI2PNG_HTML, page);
