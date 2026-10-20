// KLineChart 10.1.0 adapter. All callers pass only the rows they may reveal.
(function (root) {
  'use strict';
  const COLORS = {up: '#ef4444', down: '#22c55e', muted: '#94a3b8', ma5: '#f59e0b', ma10: '#8b5cf6', ma20: '#3b82f6', ma60: '#22c55e'};
  const DAY = 86400000, EPOCH = Date.UTC(2000, 0, 1);
  const finite = value => Number.isFinite(value) ? value : null;
  const price = value => Number.isFinite(value) ? value.toFixed(2) : '—';
  const relativeDay = day => day === 0 ? '信号日' : day < 0 ? `前${-day}日` : `+${day}`;
  const dateOf = row => (row.date || row.time || '').slice(0, 10);

  function normalize({rows = [], markers = [], showDate = true, maPeriods = [5, 10, 20]}) {
    const sums = new Map(maPeriods.map(p => [p, 0]));
    let previousTimestamp = -Infinity;
    const data = rows.map((row, i) => {
      const date = showDate ? dateOf(row) : '';
      const timestamp = date ? Date.parse(date + 'T00:00:00+08:00') : EPOCH + row.day * DAY;
      if (!Number.isFinite(timestamp) || timestamp <= previousTimestamp) {
        throw new Error('K线日期无效或未按交易日递增');
      }
      previousTimestamp = timestamp;
      for (const key of ['open', 'high', 'low', 'close']) {
        if (!Number.isFinite(row[key]) || row[key] <= 0) throw new Error('K线价格无效');
      }
      const previous = rows[i - 1]?.close;
      const bar = {timestamp, open: row.open, high: row.high, low: row.low, close: row.close,
        volume: finite(row.volume), dif: finite(row.dif), dea: finite(row.dea), hist: finite(row.hist ?? row.macd),
        label: date || relativeDay(row.day), markers: [],
        change: previous > 0 && Number.isFinite(previous) ? (row.close - previous) / previous * 100 : null};
      for (const p of maPeriods) {
        const sum = sums.get(p) + row.close - (i >= p ? rows[i - p].close : 0);
        sums.set(p, sum);
        bar['ma' + p] = Object.hasOwn(row, 'ma' + p) ? finite(row['ma' + p]) : i + 1 >= p ? sum / p : null;
      }
      return bar;
    });
    const byKey = new Map();
    rows.forEach((row, i) => {
      if (Number.isInteger(row.day)) byKey.set(row.day, data[i]);
      if (showDate && dateOf(row)) byKey.set(dateOf(row), data[i]);
    });
    const missing = [], mapped = [];
    for (const marker of markers) {
      const bar = byKey.get(Number.isInteger(marker.day) ? marker.day : dateOf(marker));
      if (!bar || !Number.isFinite(marker.price)) { missing.push(marker); continue; }
      const kind = marker.kind || 'signal';
      const label = marker.label || ({buy: 'B', sell: 'S', est: '估'}[kind] || '●');
      const text = marker.text || `${kind === 'buy' ? '买入' : kind === 'est' ? '期末估值' : kind === 'sell' ? '卖出' : label} · ${price(marker.price)} 元`;
      // Do not carry source dates or identities into anonymous charts.
      const item = {timestamp: bar.timestamp, price: marker.price, kind, label, text,
        color: marker.color || (kind === 'buy' ? COLORS.up : kind === 'sell' ? COLORS.down : COLORS.muted),
        value: kind === 'buy' ? Math.min(bar.low, marker.price) : Math.max(bar.high, marker.price)};
      mapped.push(item); bar.markers.push(item);
    }
    return {data, markers: mapped, missing};
  }

  function legends({current}) {
    if (!current) return [];
    const change = current.change;
    return [
      {title: '交易日', value: current.label},
      ...[['开盘', 'open'], ['收盘', 'close'], ['最高', 'high'], ['最低', 'low']].map(([title, key]) => ({title, value: price(current[key])})),
      {title: '涨跌幅（较前收）', value: {text: change == null ? '—' : `${change > 0 ? '+' : ''}${change.toFixed(2)}%`,
        color: change > 0 ? COLORS.up : change < 0 ? COLORS.down : COLORS.muted}},
      {title: '成交量', value: current.volume == null ? '—' : String(current.volume)},
      ...current.markers.map(m => ({title: m.label, value: {text: m.text, color: m.color}})),
    ];
  }

  const tradeStates = new WeakMap();
  const overlaps = (a, b) => a.x < b.x + b.width + 4 && a.x + a.width + 4 > b.x &&
    a.y < b.y + b.height + 4 && a.y + a.height + 4 > b.y;

  // Labels follow bar extremes, while the small dot retains the execution price.
  function tradeLayouts(chart, bounding, xAxis, yAxis, measure) {
    const state = tradeStates.get(chart);
    if (!state) return new Map();
    const range = yAxis.getRange(), space = chart.getBarSpace().bar;
    const key = [bounding.width, bounding.height, xAxis.convertToPixel(0), space, range.from, range.to].join('/');
    if (state.key === key) return state.layouts;
    const layouts = new Map(), placed = [], legendHeight = bounding.width < 480 ? 50 : 34;
    state.markers.forEach((m, markerIndex) => {
      const index = state.indices.get(m.timestamp);
      const x = xAxis.convertToPixel(index), below = m.kind === 'buy', direction = below ? 1 : -1;
      if (x < 0 || x > bounding.width) return;
      const name = m.label === 'B' ? 'B 买入' : m.label === 'S' ? 'S 卖出' : m.kind === 'est' ? '估值' : m.label;
      let text = `${name} ${price(m.price)}`;
      if (measure(text, 12, 'bold', 'sans-serif') > bounding.width - 24) {
        const prefix = Array.from(name);
        do {prefix.pop(); text = `${prefix.join('')}… ${price(m.price)}`;}
        while (prefix.length > 1 && measure(text, 12, 'bold', 'sans-serif') > bounding.width - 24);
      }
      const width = Math.min(bounding.width - 8, measure(text, 12, 'bold', 'sans-serif') + 16);
      const box = {x: Math.max(4, Math.min(bounding.width - width - 4, x - width / 2)), y: 0, width, height: 24};
      // Account for nearby candle wicks under the entire label, including after zoom.
      const radius = Math.ceil((width + 8) / space);
      let edge = yAxis.convertToPixel(m.value);
      for (let i = Math.max(0, index - radius); i <= Math.min(state.data.length - 1, index + radius); i++) {
        const bx = xAxis.convertToPixel(i);
        if (bx < box.x - space / 2 || bx > box.x + width + space / 2) continue;
        const y = yAxis.convertToPixel(below ? state.data[i].low : state.data[i].high);
        edge = below ? Math.max(edge, y) : Math.min(edge, y);
      }
      box.y = below ? edge + 16 : edge - 16 - box.height;
      box.y = Math.max(legendHeight, Math.min(bounding.height - box.height - 4, box.y));
      while (placed.some(other => overlaps(box, other))) {
        const next = box.y + direction * 28;
        if (next < legendHeight || next + box.height > bounding.height - 4) break;
        box.y = next;
      }
      placed.push(box);
      layouts.set(markerIndex, {box, text, x, y: yAxis.convertToPixel(m.price), below,
        edge: yAxis.convertToPixel(m.value)});
    });
    state.key = key; state.layouts = layouts;
    return layouts;
  }

  let registered;
  function register(lib) {
    if (registered === lib) return;
    lib.registerIndicator({name: 'STRATEGY_MA', shortName: '均线', series: 'price', calcParams: [5, 10, 20],
      regenerateFigures: periods => periods.map(p => ({key: 'ma' + p, title: 'MA' + p + ': ', type: 'line',
        styles: () => ({color: COLORS['ma' + p], size: 1})})),
      calc: data => data});
    lib.registerIndicator({name: 'STRATEGY_VOLUME', shortName: '成交量', series: 'volume', minValue: 0,
      figures: [{key: 'volume', title: '成交量: ', type: 'bar', baseValue: 0,
        styles: ({data}) => ({color: data.current?.close >= data.current?.open ? COLORS.up : COLORS.down, style: 'fill'})}],
      calc: data => data});
    // Preserve the server's warm-up, nullable values and DIF−DEA convention.
    lib.registerIndicator({name: 'STRATEGY_MACD', shortName: 'MACD(12,26,9) · 柱=DIF−DEA', precision: 6, minValue: 0, maxValue: 0,
      figures: [
        {key: 'hist', title: '柱: ', type: 'bar', baseValue: 0,
          styles: ({data}) => ({color: data.current?.hist >= 0 ? COLORS.up : COLORS.down, style: 'fill'})},
        {key: 'dif', title: 'DIF: ', type: 'line', styles: () => ({color: COLORS.ma5, size: 1})},
        {key: 'dea', title: 'DEA: ', type: 'line', styles: () => ({color: COLORS.ma10, size: 1})},
      ], calc: data => data});
    lib.registerOverlay({name: 'strategyTrade', totalStep: 1, needDefaultPointFigure: false,
      needDefaultXAxisFigure: false, needDefaultYAxisFigure: false,
      createPointFigures: ({coordinates, overlay, chart, bounding, xAxis, yAxis}) => {
        if (!coordinates.length) return [];
        const m = overlay.extendData;
        const layout = tradeLayouts(chart, bounding, xAxis, yAxis, lib.utils.calcTextWidth).get(m.markerIndex);
        if (!layout) return [];
        const {box, text, x, y, below, edge} = layout, sign = below ? 1 : -1;
        const background = tradeStates.get(chart).background, ink = tradeStates.get(chart).ink;
        const tip = edge + sign * 5, base = edge + sign * 12;
        return [
          {key: 'leader', type: 'line', attrs: {coordinates: [{x, y}, {x, y: tip},
            {x: box.x + box.width / 2, y: below ? box.y : box.y + box.height}]},
            styles: {color: m.color, size: 1, style: 'dashed', dashedValue: [3, 3]}, ignoreEvent: true},
          {key: 'arrow', type: 'polygon', attrs: {coordinates: [{x, y: tip}, {x: x - 5, y: base}, {x: x + 5, y: base}]},
            styles: {color: m.color, style: 'fill'}, ignoreEvent: true},
          {key: 'price', type: 'circle', attrs: {x, y, r: 3.5},
            styles: {color: m.color, style: 'stroke_fill', borderColor: background, borderSize: 1.5}, ignoreEvent: true},
          {key: 'badge', type: 'rect', attrs: box,
            styles: {color: background, style: 'stroke_fill', borderColor: m.color, borderSize: 1.5, borderRadius: 5}, ignoreEvent: true},
          {key: 'label', type: 'text', attrs: {x: box.x + box.width / 2, y: box.y + 12, text, align: 'center', baseline: 'middle'},
            styles: {color: ink, size: 12, weight: 'bold', family: 'sans-serif', backgroundColor: 'transparent', borderSize: 0}, ignoreEvent: true},
        ];
      }});
    registered = lib;
  }

  function mount(el, {dark = true, macd = true, maPeriods = [5, 10, 20]} = {}) {
    const lib = root.klinecharts;
    if (!el || !lib) return null;
    register(lib);
    const css = root.getComputedStyle?.(el);
    const token = (name, fallback) => css?.getPropertyValue(name).trim() || fallback;
    const ink = dark ? token('--ink', '#e9eef8') : '#172234';
    const muted = dark ? token('--muted', '#94a3b8') : '#67748a';
    const rule = dark ? token('--rule', '#293449') : '#dbe2ec';
    const background = dark ? token('--bg2', '#0d1626') : '#fff';
    const chart = lib.init(el, {locale: 'zh-CN', timezone: 'Asia/Shanghai', hotkey: {enabled: false},
      styles: {grid: {horizontal: {color: rule}, vertical: {color: rule}},
        candle: {bar: {upColor: COLORS.up, downColor: COLORS.down, noChangeColor: COLORS.muted,
          upBorderColor: COLORS.up, downBorderColor: COLORS.down, noChangeBorderColor: COLORS.muted,
          upWickColor: COLORS.up, downWickColor: COLORS.down, noChangeWickColor: COLORS.muted},
          tooltip: {showRule: 'follow_cross', showType: 'rect', title: {show: true, color: ink},
            rect: {position: 'pointer', color: dark ? token('--bg2', '#0d1626') : '#fff', borderColor: rule},
            legend: {color: ink, template: legends}},
          priceMark: {last: {upColor: COLORS.up, downColor: COLORS.down, noChangeColor: COLORS.muted}}},
        indicator: {tooltip: {title: {color: ink}, legend: {color: ink}}},
        xAxis: {tickText: {color: muted}, axisLine: {color: rule}},
        yAxis: {tickText: {color: muted}, axisLine: {color: rule}}, separator: {color: rule}}});
    if (!chart) return null;
    chart.overrideYAxis({paneId: 'candle_pane', gap: {top: 0, bottom: 0}, createRange: ({defaultRange}) => {
      const state = tradeStates.get(chart);
      if (!state) return defaultRange;
      const {width, height} = chart.getSize('candle_pane', 'main');
      let top = (width < 480 ? 90 : 74) + Math.max(0, state.above - 1) * 28;
      let bottom = 44 + Math.max(0, state.below - 1) * 28;
      const scale = Math.min(1, Math.max(0, height - 60) / (top + bottom));
      top *= scale; bottom *= scale;
      const available = Math.max(1, height - top - bottom);
      const span = defaultRange.realRange || Math.max(Math.abs(defaultRange.realFrom) * 0.02, 0.01);
      const realFrom = defaultRange.realFrom - span * bottom / available;
      const realTo = defaultRange.realTo + span * top / available;
      return {...defaultRange, realFrom, realTo, realRange: realTo - realFrom};
    }});
    if (maPeriods.length) chart.createIndicator({name: 'STRATEGY_MA', calcParams: maPeriods, paneId: 'candle_pane'}, true);
    chart.createIndicator({name: 'STRATEGY_VOLUME', paneId: 'strategy_volume'});
    if (macd) chart.createIndicator({name: 'STRATEGY_MACD', paneId: 'strategy_macd'});
    let data = [], byTime = new Map(), identity, alive = true;
    chart.setFormatter({formatDate: ({timestamp}) => byTime.get(timestamp)?.label || ''});
    chart.setDataLoader({getBars: ({type, callback}) => callback(type === 'init' ? data : [], false)});
    chart.setSymbol({ticker: '日K', pricePrecision: 2, volumePrecision: 0});
    chart.setPeriod({type: 'day', span: 1});
    const resize = () => {
      if (!alive || !el.clientWidth || !el.clientHeight) return;
      chart.setPaneOptions({id: 'strategy_volume', height: Math.max(54, Math.round(el.clientHeight * 0.16)), minHeight: 45});
      if (macd) chart.setPaneOptions({id: 'strategy_macd', height: Math.max(100, Math.round(el.clientHeight * 0.26)), minHeight: 80});
      chart.resize();
    };
    const observer = new ResizeObserver(resize);
    observer.observe(el);
    const keydown = event => {
      if (event.target !== el || event.altKey || event.ctrlKey || event.metaKey) return;
      switch (event.key) {
        case 'ArrowLeft': chart.scrollByDistance(80, 0); break;
        case 'ArrowRight': chart.scrollByDistance(-80, 0); break;
        case '+': case '=': chart.zoomAtCoordinate(1.15); break;
        case '-': chart.zoomAtCoordinate(0.85); break;
        case 'Home': chart.scrollToDataIndex(0, 0); break;
        case 'End': chart.scrollToRealTime(0); break;
        default: return;
      }
      event.preventDefault();
    };
    el.tabIndex = 0;
    el.setAttribute('aria-keyshortcuts', 'ArrowLeft ArrowRight + - Home End');
    el.addEventListener('keydown', keydown);
    resize();
    return {
      getDom: () => el,
      resize,
      setData(options) {
        if (!alive) return [];
        const normalized = normalize({...options, maPeriods});
        data = normalized.data;
        byTime = new Map(data.map(bar => [bar.timestamp, bar]));
        const counts = new Map(); let above = 0, below = 0;
        for (const marker of normalized.markers) {
          const buy = marker.kind === 'buy', key = marker.timestamp + '/' + buy;
          const count = (counts.get(key) || 0) + 1; counts.set(key, count);
          if (buy) below = Math.max(below, count); else above = Math.max(above, count);
        }
        tradeStates.set(chart, {data, markers: normalized.markers, indices: new Map(data.map((bar, i) => [bar.timestamp, i])), background, ink, above, below});
        chart.removeOverlay({groupId: 'strategy_trades'});
        chart.setStyles({candle: {tooltip: {title: {template: options.title || '日K'}}}});
        chart.resetData();
        for (const [markerIndex, marker] of normalized.markers.entries()) chart.createOverlay({name: 'strategyTrade', groupId: 'strategy_trades',
          lock: true, points: [{timestamp: marker.timestamp, value: marker.price}], extendData: {...marker, markerIndex},
          onRightClick: ({event}) => {event?.preventDefault?.();}});
        if (identity !== options.identity) {
          identity = options.identity;
          chart.setBarSpace(Math.max(2, Math.min(12, (el.clientWidth - 70) / Math.max(1, Math.min(data.length, el.clientWidth < 600 ? 60 : 120)))));
          chart.scrollToRealTime(0);
        }
        resize();
        return normalized.missing;
      },
      clear() { data = []; byTime.clear(); tradeStates.delete(chart); chart.removeOverlay({groupId: 'strategy_trades'}); chart.resetData(); },
      dispose() {
        if (!alive) return;
        alive = false; tradeStates.delete(chart); observer.disconnect(); el.removeEventListener('keydown', keydown); lib.dispose(el);
      },
    };
  }
  root.StrategyKline = {mount, normalize, legends, COLORS};
})(globalThis);
