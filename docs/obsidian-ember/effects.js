/** Framework-free effects. Every attachment returns cleanup for unmounts. */
const prefersReduced = () => matchMedia('(prefers-reduced-motion: reduce)')
const allowed = element => !prefersReduced().matches && !document.hidden && !element.closest('[data-oe-motion="off"]')

export function attachTilt(element, { angle = 6 } = {}) {
  const reduced = prefersReduced(), pointer = matchMedia('(hover: hover) and (pointer: fine)')
  const properties = ['--oe-tilt-x', '--oe-tilt-y', '--oe-light-x', '--oe-light-y', '--oe-light-opacity']
  const before = properties.map(name => [name, element.style.getPropertyValue(name), element.style.getPropertyPriority(name)])
  const hadTilt = element.classList.contains('oe-tilt')
  element.classList.add('oe-tilt')
  let frame = 0, position
  function reset() {
    cancelAnimationFrame(frame); frame = 0
    element.classList.remove('is-tracking')
    for (const [name, value, priority] of before) {
      if (value) element.style.setProperty(name, value, priority)
      else element.style.removeProperty(name)
    }
  }
  function paint() {
    frame = 0
    if (!allowed(element) || !pointer.matches || !position) { reset(); return }
    const box = element.getBoundingClientRect()
    if (!box.width || !box.height) return
    const x = Math.max(0, Math.min(1, (position.x - box.left) / box.width)), y = Math.max(0, Math.min(1, (position.y - box.top) / box.height))
    element.classList.add('is-tracking')
    element.style.setProperty('--oe-tilt-x', `${(0.5 - y) * angle * 2}deg`)
    element.style.setProperty('--oe-tilt-y', `${(x - 0.5) * angle * 2}deg`)
    element.style.setProperty('--oe-light-x', `${x * 100}%`)
    element.style.setProperty('--oe-light-y', `${y * 100}%`)
    element.style.setProperty('--oe-light-opacity', '1')
  }
  function move(event) {
    if (event.pointerType === 'touch') return
    position = { x: event.clientX, y: event.clientY }
    if (!frame) frame = requestAnimationFrame(paint)
  }
  const owner = element.closest('.oe-theme') || element
  const observer = new MutationObserver(() => { if (!allowed(element)) reset() })
  observer.observe(owner, { attributes: true, attributeFilter: ['data-oe-motion', 'data-oe-theme'] })
  element.addEventListener('pointermove', move)
  element.addEventListener('pointerleave', reset)
  element.addEventListener('pointercancel', reset)
  reduced.addEventListener('change', reset)
  pointer.addEventListener('change', reset)
  document.addEventListener('visibilitychange', reset)
  return () => {
    reset(); observer.disconnect()
    if (!hadTilt) element.classList.remove('oe-tilt')
    element.removeEventListener('pointermove', move)
    element.removeEventListener('pointerleave', reset)
    element.removeEventListener('pointercancel', reset)
    reduced.removeEventListener('change', reset)
    pointer.removeEventListener('change', reset)
    document.removeEventListener('visibilitychange', reset)
  }
}

export function attachReveal(root, selector = '[data-oe-reveal]') {
  const nodes = [...root.querySelectorAll(selector)], reduced = prefersReduced()
  if (!allowed(root) || !('IntersectionObserver' in window)) return () => {}
  function finish(node) { node.classList.remove('oe-reveal-pending'); node.classList.add('oe-reveal'); observer.unobserve(node) }
  const observer = new IntersectionObserver(entries => { for (const entry of entries) if (entry.isIntersecting) finish(entry.target) }, { threshold: .08 })
  for (const node of nodes) { node.classList.add('oe-reveal-pending'); observer.observe(node) }
  function clear() { observer.disconnect(); for (const node of nodes) node.classList.remove('oe-reveal-pending', 'oe-reveal') }
  function changed() { if (!allowed(root)) clear() }
  reduced.addEventListener('change', changed)
  const attributes = new MutationObserver(changed)
  attributes.observe(root, { attributes: true, attributeFilter: ['data-oe-motion'] })
  return () => { clear(); attributes.disconnect(); reduced.removeEventListener('change', changed) }
}

let currentTransition
export function cancelTransition() {
  const job = currentTransition; currentTransition = undefined
  job?.cancel(); job?.scope.classList.remove('oe-transition-scope')
  delete document.documentElement.dataset.oeTransition
}

/** update may await a framework flush or synchronous WebGL first draw, never a RAF. */
export async function transitionView(scope, update, { direction = 'forward' } = {}) {
  cancelTransition()
  if (!allowed(scope) || !document.startViewTransition) { await update(); return }
  const root = document.documentElement, reduced = prefersReduced()
  let transition, failure
  const job = { scope, cancel: () => transition?.skipTransition() }
  currentTransition = job
  scope.classList.add('oe-transition-scope')
  root.dataset.oeTransition = direction === 'back' ? 'back' : 'forward'
  const cancel = () => job.cancel()
  reduced.addEventListener('change', cancel)
  document.addEventListener('visibilitychange', cancel)
  window.addEventListener('resize', cancel)
  try {
    transition = document.startViewTransition(async () => {
      if (currentTransition !== job) return
      try { await update() } catch (error) { failure = error; throw error }
    })
    void transition.ready.catch(() => {}) // Skipping snapshots during rapid input is expected.
    void transition.updateCallbackDone.catch(() => {})
    await transition.finished.catch(error => { if (failure) throw error })
  } finally {
    reduced.removeEventListener('change', cancel)
    document.removeEventListener('visibilitychange', cancel)
    window.removeEventListener('resize', cancel)
    if (currentTransition === job) {
      currentTransition = undefined
      delete root.dataset.oeTransition
      scope.classList.remove('oe-transition-scope')
    } else if (!currentTransition?.scope || currentTransition.scope !== scope) scope.classList.remove('oe-transition-scope')
  }
}
