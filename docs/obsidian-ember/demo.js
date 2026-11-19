import { attachTilt, attachReveal, transitionView, cancelTransition } from './effects.js'

const theme = document.body, scope = document.getElementById('surfaces')
const cleanups = [attachTilt(document.getElementById('tilt-surface')), attachReveal(theme)]
const media = matchMedia('(prefers-reduced-motion: reduce)')
const colors = [['bg', '页面底色'], ['surface', '面板'], ['surface-alt', '次级表面'], ['accent', '主要操作'], ['success', '成功状态'], ['warning', '提醒状态']]
document.getElementById('swatches').innerHTML = colors.map(([key, name]) => `<div class="demo-swatch" style="--swatch:var(--oe-${key})"><i aria-hidden="true"></i><span>${name}</span><code data-color="${key}"></code></div>`).join('')
function swatches() { for (const node of document.querySelectorAll('[data-color]')) node.textContent = getComputedStyle(theme).getPropertyValue(`--oe-${node.dataset.color}`).trim().toUpperCase() }
function preference() { document.getElementById('reduced-state').textContent = media.matches ? '已启用' : '未启用' }
swatches(); preference(); media.addEventListener('change', preference)
document.getElementById('theme').onclick = event => {
  cancelTransition()
  theme.dataset.oeTheme = theme.dataset.oeTheme === 'dark' ? 'light' : 'dark'
  event.currentTarget.textContent = theme.dataset.oeTheme === 'dark' ? '切换浅色' : '切换深色'
  swatches()
}
document.getElementById('motion').onclick = event => {
  cancelTransition()
  theme.dataset.oeMotion = theme.dataset.oeMotion === 'on' ? 'off' : 'on'
  event.currentTarget.textContent = theme.dataset.oeMotion === 'on' ? '动效：开启' : '动效：关闭'
  event.currentTarget.setAttribute('aria-pressed', String(theme.dataset.oeMotion === 'on'))
}
const views = [
  ['悬停体验', '有层次的内容表面', '移动鼠标查看轻微透视和随光变化。表面可承载任何项目中的信息。', '立体表面'],
  ['切换体验', '自然进入下一层', '旧内容退后，新内容以轻微透视展开；旁边的说明面板同步进入。', '景深切换'],
]
for (const button of document.querySelectorAll('[data-view]')) button.onclick = async () => {
  const index = Number(button.dataset.view)
  try {
    await transitionView(scope, () => {
      const [label, title, copy, aside] = views[index]
      document.getElementById('surface-label').textContent = label
      document.getElementById('surface-title').textContent = title
      document.getElementById('surface-copy').textContent = copy
      document.getElementById('side-title').textContent = aside
      for (const item of document.querySelectorAll('[data-view]')) item.setAttribute('aria-pressed', String(item === button))
    }, { direction: index ? 'forward' : 'back' })
  } catch { document.querySelector('.demo-status').textContent = '当前浏览器未能播放切换效果。' }
}
const dialog = document.getElementById('sample-dialog')
document.getElementById('open-dialog').onclick = () => { cancelTransition(); dialog.showModal() }
for (const button of dialog.querySelectorAll('[data-close]')) button.onclick = () => dialog.close()
dialog.onclick = event => { if (event.target === dialog) dialog.close() }
for (const button of document.querySelectorAll('[data-scroll]')) button.onclick = () => document.getElementById(button.dataset.scroll).scrollIntoView({ behavior: 'instant', block: 'start' })
document.querySelector('form').onsubmit = event => { event.preventDefault(); document.querySelector('.demo-status').textContent = '示例已响应，不会提交到服务器。' }
function dispose() { cancelTransition(); cleanups.forEach(cleanup => cleanup()); media.removeEventListener('change', preference) }
window.addEventListener('pagehide', dispose, { once: true })
