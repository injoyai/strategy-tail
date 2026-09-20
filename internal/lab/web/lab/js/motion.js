// GSAP 动效层：仅建立工作流层级（首屏、Tab 切换、状态条与结果揭示）。
// 合同：只动 autoAlpha/x/y，0.30–0.45s，power2.out，stagger≈0.045；
// prefers-reduced-motion 时整体禁用；GSAP 未加载（CDN 不可用）时功能完整可用。

let motionReady = false;
let motionMM = null;

// animatePanel 面板浮入（Tab 切换 / 首屏主面板）。
export function animatePanel(panel) {
  if (!motionReady || !panel || typeof gsap === 'undefined') return;
  gsap.killTweensOf(panel);
  gsap.fromTo(panel, {autoAlpha: 0, y: 12}, {
    autoAlpha: 1, y: 0, duration: .36, ease: 'power2.out', overwrite: 'auto',
    clearProps: 'opacity,visibility,transform'
  });
}

// animateStatus 状态条揭示（任务启动/完成提示）。
export function animateStatus(bar) {
  if (!motionReady || typeof gsap === 'undefined') return;
  gsap.fromTo(bar, {autoAlpha: 0, y: 10}, {
    autoAlpha: 1, y: 0, duration: .3, ease: 'power2.out', overwrite: 'auto',
    clearProps: 'opacity,visibility,transform'
  });
}

// animateFactorResult 因子研究结果区揭示：容器内关键块按序浮入。
export function animateFactorResult(root) {
  if (!motionReady || !root || typeof gsap === 'undefined') return;
  const items = root.querySelectorAll('.summary-line,#quintileChart,.result-actions,.coverage,.warnbar,.year-title,.year-wrap,details.stat');
  if (!items.length) return;
  gsap.killTweensOf(items);
  gsap.fromTo(items, {autoAlpha: 0, y: 8}, {
    autoAlpha: 1, y: 0, duration: .32, stagger: .045, ease: 'power2.out', overwrite: 'auto',
    clearProps: 'opacity,visibility,transform'
  });
}

// initMotion 首屏编排：品牌区 → Tab 条 → 当前面板。挂在根组件 mounted 之后调用。
export function initMotion() {
  if (typeof gsap === 'undefined') return;
  motionMM = gsap.matchMedia();
  motionMM.add({allowMotion: '(prefers-reduced-motion: no-preference)'}, context => {
    if (!context.conditions.allowMotion) return;
    motionReady = true;
    gsap.fromTo('.brand-mark,.brand-copy,.header-meta', {autoAlpha: 0, y: -8}, {
      autoAlpha: 1, y: 0, duration: .45, stagger: .07, ease: 'power2.out',
      clearProps: 'opacity,visibility,transform'
    });
    gsap.fromTo('.tabs [role="tab"]', {autoAlpha: 0, y: -5}, {
      autoAlpha: 1, y: 0, duration: .34, delay: .08, stagger: .045, ease: 'power2.out',
      clearProps: 'opacity,visibility,transform'
    });
    // 面板用 v-show（display:none）控制可见性，选择当前可见面板做揭示动画
    const panels = [...document.querySelectorAll('.tab-panel')];
    animatePanel(panels.find(p => p.offsetParent !== null) || panels[0] || null);
    return () => { motionReady = false; };
  });
}
