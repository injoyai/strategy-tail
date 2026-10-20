import { markRaw } from './vendor/vue.esm-browser.prod.js';

const number = digits => cell => {
  const value = cell.getValue();
  return Number.isFinite(value) ? value.toFixed(digits) : '—';
};
const signed = digits => cell => {
  const node = document.createElement('span'), value = cell.getValue();
  node.textContent = number(digits)(cell);
  node.className = value > 0 ? 'pos' : value < 0 ? 'neg' : '';
  return node;
};

// Tabulator 独占宿主 DOM；数据由 Vue 单向投影，不启用双向 reactiveData。
export function mountTradeTable(el, onSelect, onError) {
  if (!globalThis.Tabulator) throw new Error('表格组件未加载，请刷新页面后重试');
  let ready = false, destroyed = false, rows = [], selected = null;
  const titleFormatter = cell => {
    const button = document.createElement('button');
    button.type = 'button'; button.className = 'trade-sort-button';
    button.textContent = cell.getValue();
    button.setAttribute('aria-label', '按' + cell.getValue() + '排序');
    return button;
  };
  const table = new globalThis.Tabulator(el, {
    height: 520, layout: 'fitData', index: '_rowId',
    placeholder: '无匹配交易', selectableRows: false,
    columnDefaults: {headerHozAlign: 'center', hozAlign: 'center', resizable: false, titleFormatter},
    columns: [
      {title: '变体', field: 'variant', formatter: 'plaintext'},
      {title: '代码', field: 'code', formatter(cell) {
        const t = cell.getRow().getData(), button = document.createElement('button');
        button.type = 'button'; button.className = 'secondary trade-kline-button';
        button.textContent = t.code + ' · K线';
        button.setAttribute('aria-label', `查看 ${t.code} ${t.buyTime} 交易K线`);
        button.setAttribute('aria-controls', 'tradeKlinePanel');
        button.addEventListener('click', e => {e.stopPropagation(); onSelect(t);});
        return button;
      }},
      {title: '买入时间', field: 'buyTime', formatter: 'plaintext'},
      {title: '买入价', field: 'buyPrice', sorter: 'number', formatter: number(2)},
      {title: '卖出时间', field: 'sellTime', formatter: 'plaintext'},
      {title: '卖出价', field: 'sellPrice', sorter: 'number', formatter: number(2)},
      {title: '数量', field: 'quantity', sorter: 'number'},
      {title: '盈亏(元)', field: 'profit', sorter: 'number', formatter: signed(0)},
      {title: '收益率%', field: 'rate', sorter: 'number', formatter: signed(2)},
      {title: '持仓天', field: 'holdingDays', sorter: 'number'},
      {title: '成交类型', field: 'virtual', formatter: cell => cell.getValue() === true ? '期末估值（Virtual）' : cell.getValue() === false ? '实际成交' : '未记录'},
    ],
    rowFormatter(row) {
      const active = row.getData()._rowId === selected;
      row.getElement().classList.toggle('selected', active);
      row.getElement().setAttribute('aria-selected', String(active));
    },
  });
  function update() {
    if (ready && !destroyed) table.replaceData(rows).catch(e => {if (!destroyed) onError(e);});
  }
  table.on('tableBuilt', () => {ready = true; update();});
  table.on('rowClick', (_e, row) => onSelect(row.getData()));
  return markRaw({
    setData(data) {rows = data; update();},
    select(id) {
      const previous = selected;
      selected = id;
      if (ready) table.getRows().forEach(row => {
        if (row.getData()._rowId === previous || row.getData()._rowId === selected) row.reformat();
      });
    },
    resize() {if (ready && !destroyed) table.redraw(true);},
    destroy() {destroyed = true; table.destroy();},
  });
}
