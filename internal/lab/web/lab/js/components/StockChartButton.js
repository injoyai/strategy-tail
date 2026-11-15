import { stockLabel } from '../format.js';
import { state } from '../store.js';

export default {
  name: 'StockChartButton',
  inject: ['openStockChart'],
  props: {
    code: {type: String, default: ''},
    name: {type: String, default: ''},
  },
  computed: {
    label() { return stockLabel(this.code, this.name ? {[this.code]: this.name} : state.stockNames); },
  },
  template: `
<button v-if="code" type="button" class="stock-chart-button" aria-haspopup="dialog" aria-controls="stock-chart-dialog"
  :aria-label="'查看 ' + label + ' 日K线'" @click.stop="openStockChart(code, name)">{{ label }}</button>
<span v-else>—</span>
  `,
};
