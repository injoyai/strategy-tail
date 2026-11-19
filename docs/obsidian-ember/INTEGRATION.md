# 接入示例

## 原生 HTML / JavaScript

先按 README 引入三个 CSS 文件，并把项目放在 `.oe-theme` 内。

```html
<section id="card" class="oe-panel">
  <div class="oe-tilt-layer">你的内容</div>
</section>
<div id="workspace">
  <main class="oe-transition-plane">主要内容</main>
  <aside class="oe-transition-panel">辅助信息</aside>
</div>
```

```js
import { attachTilt, attachReveal, transitionView, cancelTransition } from './effects.js'

const removeTilt = attachTilt(document.querySelector('#card'), { angle: 6 })
const removeReveal = attachReveal(document.querySelector('.oe-theme'))

// 一个 scope 中，每种 view-transition 角色最多一个，避免命名冲突。
await transitionView(document.querySelector('#workspace'), () => {
  // 更新你的内容；可 await 框架渲染刷新，不要 await 网络请求或动画帧。
}, { direction: 'forward' })

// 用户切换主题、主动关闭动画、离开页面时可立即取消快照。
cancelTransition()
// 移除组件时调用：
removeTilt()
removeReveal()
```

`attachReveal(root)` 默认查找 `[data-oe-reveal]`，由 IntersectionObserver 触发进入；不监听页面 scroll。

`transitionView()` 返回 Promise。普通使用会自动处理原生 API 不支持、快速连续切换、系统减少动态效果、页面隐藏与窗口尺寸变化。调用方应处理更新函数抛出的异常。若用你自己的动效开关，先 `cancelTransition()`，再修改主题根容器的 `data-oe-motion`。

浏览器捕捉 View Transition 快照时会暂停普通 RAF 与 ResizeObserver 回调。如果新内容依赖 Canvas/WebGL，须同步设置尺寸并绘制首帧，之后再完成更新函数。不要在更新函数中等待 `requestAnimationFrame()`，否则可能卡住过渡。

## Vue 3

```vue
<script setup>
import { onMounted, onBeforeUnmount, ref, nextTick } from 'vue'
import { attachTilt, transitionView, cancelTransition } from './effects.js'
const card = ref(), workspace = ref(), mode = ref('summary')
let removeTilt
onMounted(() => { removeTilt = attachTilt(card.value) })
onBeforeUnmount(() => { cancelTransition(); removeTilt?.() })
async function switchMode(value) {
  await transitionView(workspace.value, async () => {
    mode.value = value
    await nextTick()
  })
}
</script>
```

将 `ref="card"` 放在独立表面，将 `ref="workspace"` 放在含 `.oe-transition-plane` / `.oe-transition-panel` 的工作区。切换函数应按你的模板更新内容。

## React

```jsx
import { useEffect, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { attachTilt, transitionView, cancelTransition } from './effects.js'

function Workspace() {
  const root = useRef(null), card = useRef(null)
  const [mode, setMode] = useState('summary')
  useEffect(() => attachTilt(card.current), [])
  useEffect(() => () => cancelTransition(), [])
  const change = value => transitionView(root.current, () => {
    flushSync(() => setMode(value))
  })
  return <div ref={root}>
    <button onClick={() => change(mode === 'summary' ? 'detail' : 'summary')}>切换</button>
    <section ref={card} className="oe-panel oe-transition-plane">{mode}</section>
    <aside className="oe-panel oe-transition-panel">辅助信息</aside>
  </div>
}
```

CSS 通过项目现有入口导入。React / Vue 仅为接入示例，不是此包的运行依赖。

## 可选 Three.js

只有需要 WebGL 时才安装：

```sh
npm install three@0.187.0
```

在项目打包器中使用：

```js
import { createThreeSurface, createLayerSeparation, materialPresets } from './three-scene.js'

const surface = createThreeSurface(container, {
  theme: 'dark',
  build(THREE, { scene, camera }) {
    // 返回使用方自己创建的 Object3D；包本身不提供任何模型。
    // 可用 new THREE.MeshStandardMaterial(materialPresets.silver) 创建材质。
    return yourOwnedContent
  },
  animate({ elapsed, delta, content, scene, camera }) {
    // 可选。若需要连续动画，在此更新自己的内容。
    // 自动尊重减少动态效果、页面隐藏、离屏状态和 motion 开关。
  },
})
surface.setTheme('light')
surface.setMotion(false)
surface.focus(yourOwnedContent) // 自动按包围盒聚焦；关闭动效时立即完成
surface.invalidate() // 内容改变后请求绘制
surface.resize()     // 必要时同步调整尺寸并绘制，用于切换快照
surface.dispose()    // 页面 / 组件卸载时必须调用
```

通用分层 / 合拢不依赖对象类型：

```js
const layers = createLayerSeparation(surface, [
  { object: firstOwnedLayer, offset: [0, 1.2, 0] },
  { object: secondOwnedLayer, offset: [0, -.5, 0] },
])
layers.set(1) // 分层，约 700ms
layers.set(0) // 合拢；快速反向从当前变换继续
layers.dispose() // 恢复初始位置；在 surface.dispose() 前调用
```

也可用 `surface.tween(progress => { /* 更新自己的视觉变换 */ }, { duration: 700 })`，返回值是取消函数。系统减少动态效果和 `setMotion(false)` 会把当前视觉插值直接置于终点。聚焦时用户拖动会取消相机插值，不和输入争夺相机。

`container` 必须有非零尺寸，例如 `height:400px`。`build()` 同步返回当前视口独占的对象。不要传入与其他视口共享的几何、材质、纹理或骨骼：`dispose()` 会释放返回对象拥有的这些资源。自有模型的网络加载、失败状态、业务权限与取消仍由项目负责。

不提供 `animate` 时，静态内容按需绘制，拖动阻尼结束后停止 RAF。调用方修改内容后用 `invalidate()` 请求刷新。主题只改变环境曝光和灯光，不会覆盖使用方的材质色。

如果浏览器无法创建 WebGL，构造函数会抛出异常；在项目中捕获并显示可用的静态内容。纯 CSS 立体、颜色和其他组件仍可独立使用。

## 移植到其他设计系统

`tokens.json` 是普通语义 JSON，不冒充某个设计工具的专有导入格式。你可以映射：

```js
const theme = tokens.themes.dark
const appTheme = {
  background: theme.bg,
  panel: theme.surface,
  primary: theme.accent,
  onPrimary: theme['on-accent'],
  text: theme.text,
  muted: theme.muted,
  border: theme.border,
}
```

CSS 变量、圆角、间距和动效可分别使用，不要求保留演示页面布局。
