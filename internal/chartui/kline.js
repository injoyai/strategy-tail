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
        value: marker.outside ? (kind === 'buy' ? Math.min(bar.low, marker.price) : Math.max(bar.high, marker.price)) : marker.price};
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
      createPointFigures: ({coordinates, overlay}) => {
        if (!coordinates.length) return [];
        const m = overlay.extendData, p = coordinates[0], below = m.kind === 'buy';
        return [{type: 'text', attrs: {x: p.x, y: p.y + (below ? 14 : -14), text: `${m.label} ${price(m.price)}`,
          align: 'center', baseline: below ? 'top' : 'bottom'},
          styles: {color: m.color, size: 11, weight: 'bold', backgroundColor: 'transparent', borderSize: 0}, ignoreEvent: true}];
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
        chart.removeOverlay({groupId: 'strategy_trades'});
        chart.setStyles({candle: {tooltip: {title: {template: options.title || '日K'}}}});
        chart.resetData();
        for (const marker of normalized.markers) chart.createOverlay({name: 'strategyTrade', groupId: 'strategy_trades',
          lock: true, points: [{timestamp: marker.timestamp, value: marker.value}], extendData: marker,
          onRightClick: ({event}) => {event?.preventDefault?.();}});
        if (identity !== options.identity) {
          identity = options.identity;
          chart.setBarSpace(Math.max(2, Math.min(12, (el.clientWidth - 70) / Math.max(1, Math.min(data.length, el.clientWidth < 600 ? 60 : 120)))));
          chart.scrollToRealTime(0);
        }
        resize();
        return normalized.missing;
      },
      clear() { data = []; byTime.clear(); chart.removeOverlay({groupId: 'strategy_trades'}); chart.resetData(); },
      dispose() {
        if (!alive) return;
        alive = false; observer.disconnect(); el.removeEventListener('keydown', keydown); lib.dispose(el);
      },
    };
  }
  root.StrategyKline = {mount, normalize, legends, COLORS};
})(globalThis);
