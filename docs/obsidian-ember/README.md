# 黑曜石 + 信号橙 · 通用视觉效果包

从已确认的深色 / 浅色视觉方案提取，供其他项目复用。包含配色、基础组件、CSS 立体交互、页面景深切换，以及可选的 Three.js 灯光、阴影和材质模块。没有任何机器人、园区、摄像头业务页面、三维模型、业务数据或 API 地址。

## 文件

| 文件 | 用途 |
| --- | --- |
| `tokens.css` | 深浅主题、语义色、字体、圆角与动画参数 |
| `tokens.json` | 同一套参数的普通 JSON，适合转换为其他框架的配置 |
| `components.css` | 通用按钮、表单、面板、分段按钮、状态、表格、弹窗和窄导航 |
| `effects.css` | 透视倾斜、层叠景深、随光变化、进入动画、弹窗及视图切换 |
| `effects.js` | 无框架、无运行依赖的交互与生命周期管理 |
| `three-scene.js` | 可选 Three.js 相机、光照、环境反射、阴影、聚焦、通用分层、渲染及资源释放 |
| `three-presets.json` | 可单独复用的 Three.js 材质与灯光数值 |
| `demo.html` / `demo.js` | 不依赖构建工具的交互示例 |
| `STYLE_GUIDE.md` | 色彩用途、空间尺度、动效与移植规则 |
| `INTEGRATION.md` | 原生 JS、Vue、React 与 Three.js 接入示例 |
| `start-preview.py` | 自动选择空闲端口、启动本地示例 |

## 先看效果

解压后，在本目录运行：

```sh
python3 start-preview.py
```

Windows 可使用 `py start-preview.py`。浏览器自动打开。按 Ctrl+C 结束。此方式只需要 Python，不需要 npm、Three.js 或外部网络。

也可用现有静态服务器访问 `demo.html`。示例脚本采用 ES Modules，请通过 HTTP 打开，不能直接双击 HTML 使用 `file://` 加载模块。

## 在项目中使用

```html
<link rel="stylesheet" href="./tokens.css">
<link rel="stylesheet" href="./components.css">
<link rel="stylesheet" href="./effects.css">

<div class="oe-theme" data-oe-theme="dark" data-oe-motion="on">
  <section class="oe-panel">
    <h2 class="oe-subtitle">你的内容标题</h2>
    <p class="oe-note">替换为项目自己的内容。</p>
    <button class="oe-button oe-button--primary">保存</button>
  </section>
</div>
```

- `data-oe-theme="dark"`：黑曜石；`light`：冷银灰。未指定主题时默认深色。
- `data-oe-motion="on"`：允许动效；`off`：关闭。系统“减少动态效果”始终优先。
- `oe-` 前缀隔离变量、组件类和关键帧，不重置其他项目的 `body`、按钮或标题。
- 只想使用颜色时，仅引入 `tokens.css`。不需要组件、JavaScript 或 Three.js。
- CSS 立体效果和页面动画不依赖 Three.js。需要 WebGL 灯光和材质时，才安装可选依赖 `three@0.187.0`。

更多接入代码见 [INTEGRATION.md](INTEGRATION.md)。

## 范围与验证

通用示例包括深浅主题、透视与随光悬停、层叠表面、景深切换、弹窗进入、按钮反馈、内容进入、实时减少动画和窄屏布局。原生 View Transition 不可用时直接更新内容。交互附件提供清理函数；Three.js 模块提供 `dispose()`。

Three.js 配置提供真实环境反射与柔和阴影，不包含后期泛光、粒子系统或任何几何模型。发光材质的 `emissive` 并不等于屏幕后期泛光。不同场景大小需要调整相机、光照强度与阴影范围。

核心文件可直接复制到任意项目；`package.json` 只声明 ES Module 类型及可选 Three.js 同伴依赖，并不要求把整个目录作为 npm 包安装。
