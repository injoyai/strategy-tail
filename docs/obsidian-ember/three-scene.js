/** Optional peer dependency: three@0.187.0. No model, asset URL or business data. */
import * as THREE from 'three'
import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js'

export const materialPresets = {
  silver: { color: '#c4c8c9', metalness: .63, roughness: .31 },
  steel: { color: '#747b80', metalness: .82, roughness: .28 },
  graphite: { color: '#24292d', metalness: .5, roughness: .35 },
  matte: { color: '#16191c', metalness: .05, roughness: .9 },
  ember: { color: '#ef8447', metalness: .5, roughness: .32, emissive: '#9a3e17', emissiveIntensity: .28 },
  glow: { color: '#f5ad7d', metalness: .25, roughness: .2, emissive: '#da632b', emissiveIntensity: 1.3 },
}

/** build returns an owned Object3D. This helper disposes its geometry/materials/textures. */
export function createThreeSurface(host, { build, animate, theme = 'dark', motion = true } = {}) {
  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true })
  let controls, environment, resize, intersection, observer, content, frame = 0, disposed = false, visible = true
  const reduced = matchMedia('(prefers-reduced-motion: reduce)')
  const scene = new THREE.Scene(), camera = new THREE.PerspectiveCamera(35, 1, .1, 100)
  const tweens = new Set()
  let cancelFocus
  const sky = new THREE.HemisphereLight('#e9eff4', '#33302d', 1.4)
  let elapsed = 0, previous = 0
  const moving = () => motion && !reduced.matches && host.closest('[data-oe-motion="off"]') === null
  function schedule() { if (!disposed && visible && !document.hidden && !frame) frame = requestAnimationFrame(draw) }
  function draw(now) {
    frame = 0
    if (disposed || !visible || document.hidden) return
    const dt = Math.min(.05, previous ? (now - previous) / 1000 : 0); previous = now
    if (moving() && animate) { elapsed += dt; animate({ elapsed, delta: dt, content, scene, camera }) }
    for (const job of tweens) {
      job.elapsed += dt * 1000
      const progress = moving() ? Math.min(1, job.elapsed / job.duration) : 1
      job.update(1 - (1 - progress) ** 3)
      if (progress === 1) tweens.delete(job)
    }
    const settling = controls.update(dt || 1 / 60)
    renderer.render(scene, camera)
    if (moving() && animate || settling || tweens.size) schedule()
  }
  function appearance() {
    renderer.toneMappingExposure = theme === 'dark' ? 1 : 1.1
    sky.intensity = theme === 'dark' ? 1.4 : 2.1
    controls.enableDamping = moving()
    if (!moving()) { for (const job of tweens) job.update(1); tweens.clear() }
    cancelAnimationFrame(frame); frame = 0; previous = 0; schedule()
  }
  function visibilityChanged() { cancelAnimationFrame(frame); frame = 0; previous = 0; if (!document.hidden) schedule() }
  function size() {
    const width = Math.max(1, host.clientWidth), height = Math.max(1, host.clientHeight)
    renderer.setPixelRatio(Math.min(devicePixelRatio || 1, 1.6))
    renderer.setSize(width, height); camera.aspect = width / height; camera.updateProjectionMatrix()
    renderer.render(scene, camera) // Also works inside a View Transition update callback.
    schedule()
  }
  function tween(update, { duration = 700 } = {}) {
    if (disposed) return () => {}
    if (!moving() || !Number.isFinite(duration) || duration <= 0) { update(1); schedule(); return () => {} }
    const job = { update, duration, elapsed: 0 }; tweens.add(job); schedule()
    return () => tweens.delete(job)
  }
  function focus(object, { duration = 700, padding = 1.25 } = {}) {
    cancelFocus?.()
    const bounds = new THREE.Box3().setFromObject(object)
    if (bounds.isEmpty()) return () => {}
    const center = bounds.getCenter(new THREE.Vector3()), radius = bounds.getSize(new THREE.Vector3()).length() / 2
    const direction = camera.position.clone().sub(controls.target).normalize()
    const distance = radius / Math.sin(THREE.MathUtils.degToRad(camera.fov / 2)) * Math.max(1, 1 / camera.aspect) * padding
    const start = camera.position.clone(), target = controls.target.clone(), destination = center.clone().addScaledVector(direction, distance)
    cancelFocus = tween(progress => { camera.position.lerpVectors(start, destination, progress); controls.target.lerpVectors(target, center, progress) }, { duration })
    return cancelFocus
  }
  function interaction() { cancelFocus?.() }
  function dispose() {
    if (disposed) return
    disposed = true; cancelAnimationFrame(frame); frame = 0; tweens.clear()
    resize?.disconnect(); intersection?.disconnect(); observer?.disconnect()
    document.removeEventListener('visibilitychange', visibilityChanged)
    reduced.removeEventListener('change', appearance)
    controls?.removeEventListener('change', schedule); controls?.removeEventListener('start', interaction); controls?.dispose()
    const geometries = new Set(), materials = new Set(), textures = new Set()
    content?.traverse(object => {
      if (object.geometry) geometries.add(object.geometry)
      for (const material of !object.material ? [] : Array.isArray(object.material) ? object.material : [object.material]) {
        materials.add(material)
        for (const value of Object.values(material)) if (value instanceof THREE.Texture) textures.add(value)
      }
      object.skeleton?.dispose()
    })
    for (const texture of textures) texture.dispose()
    for (const material of materials) material.dispose()
    for (const geometry of geometries) geometry.dispose()
    scene.traverse(object => object.shadow?.dispose())
    environment?.dispose(); renderer.dispose(); renderer.domElement.remove(); scene.clear()
  }
  try {
    renderer.shadowMap.enabled = true; renderer.shadowMap.type = THREE.PCFSoftShadowMap
    renderer.outputColorSpace = THREE.SRGBColorSpace; renderer.toneMapping = THREE.ACESFilmicToneMapping
    renderer.domElement.style.cssText = 'display:block;width:100%;height:100%;touch-action:none'
    host.append(renderer.domElement)
    camera.position.set(7.7, 5.4, 9.2)
    controls = new OrbitControls(camera, renderer.domElement)
    controls.target.set(0, .9, 0); controls.enablePan = false
    controls.minDistance = .5; controls.maxDistance = 22; controls.maxPolarAngle = Math.PI * .47
    scene.add(sky)
    const key = new THREE.DirectionalLight('#f0f2f3', 4)
    key.position.set(-4, 9, 7); key.castShadow = true; key.shadow.mapSize.set(1024, 1024)
    Object.assign(key.shadow.camera, { left: -7, right: 7, top: 7, bottom: -7, near: 1, far: 30 })
    key.shadow.bias = -.0005; scene.add(key)
    const rim = new THREE.DirectionalLight('#f4a36b', 3.6); rim.position.set(5, 5, -6); scene.add(rim)
    const fill = new THREE.DirectionalLight('#a9bed0', 1.7); fill.position.set(-8, 3, -4); scene.add(fill)
    const room = new RoomEnvironment(), generator = new THREE.PMREMGenerator(renderer)
    try { environment = generator.fromScene(room); scene.environment = environment.texture }
    finally { room.dispose(); generator.dispose() }
    if (build) { content = build(THREE, { scene, camera }); if (content) scene.add(content) }
    controls.addEventListener('change', schedule)
    controls.addEventListener('start', interaction)
    resize = new ResizeObserver(size); resize.observe(host)
    intersection = new IntersectionObserver(entries => { visible = entries[0].isIntersecting; visibilityChanged() }); intersection.observe(host)
    observer = new MutationObserver(appearance)
    observer.observe(host.closest('.oe-theme') || host, { attributes: true, attributeFilter: ['data-oe-motion'] })
    reduced.addEventListener('change', appearance); document.addEventListener('visibilitychange', visibilityChanged)
    appearance(); controls.update(); size()
    return { scene, camera, renderer, controls, tween, focus, invalidate: schedule, resize: size, setTheme(value) { theme = value; appearance() }, setMotion(value) { motion = value; appearance() }, dispose }
  } catch (error) { dispose(); throw error }
}

/** Separate any set of owned objects, without knowing their shape or business meaning. */
export function createLayerSeparation(surface, layers) {
  const bases = layers.map(({ object }) => object.position.clone())
  let cancel
  return {
    set(amount, { duration = 700 } = {}) {
      cancel?.()
      const starts = layers.map(({ object }) => object.position.clone())
      const goals = layers.map(({ offset }, i) => bases[i].clone().addScaledVector(new THREE.Vector3(...offset), amount))
      cancel = surface.tween(progress => layers.forEach(({ object }, i) => object.position.lerpVectors(starts[i], goals[i], progress)), { duration })
    },
    dispose() { cancel?.(); layers.forEach(({ object }, i) => object.position.copy(bases[i])); surface.invalidate() },
  }
}
