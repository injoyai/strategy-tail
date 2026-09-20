// 原生 <dialog> 通用行为：open ⇄ showModal/close 同步；close 事件
// （Esc / backdrop / ×）统一回写 open=false。各对话框覆写 closeDlg 或
// 追加 watch 处理表单清理。
export const dlgMixin = {
  data() {
    return {open: false};
  },
  watch: {
    open(v) {
      const dlg = this.$refs.dlg;
      if (!dlg) return;
      if (v && !dlg.open) dlg.showModal();
      else if (!v && dlg.open) dlg.close();
    },
  },
  mounted() {
    const dlg = this.$refs.dlg;
    if (dlg) dlg.addEventListener('close', () => { this.open = false; });
  },
  methods: {
    closeDlg() { this.open = false; },
  },
};
