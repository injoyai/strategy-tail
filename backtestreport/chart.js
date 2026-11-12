(() => {
  "use strict";

  const hour = 3_600_000;
  const day = 24 * hour;
  const offset = 8 * hour;
  const colors = ["#16669d", "#9a590c", "#6c4a9d", "#267675", "#a03e67", "#526a30", "#475a76", "#876044"];
  const shapes = ["●", "▲", "◆", "■"];
  const muted = "#50627a";
  const numbers = new Intl.NumberFormat("zh-CN", { maximumSignificantDigits: 10 });
  const clamp = (value, min, max) => Math.max(min, Math.min(value, max));

  function lowerBound(items, time, key) {
    let low = 0, high = items.length;
    while (low < high) {
      const middle = Math.floor((low + high) / 2);
      if (key(items[middle]) < time) low = middle + 1;
      else high = middle;
    }
    return low;
  }

  const timestamp = (point) => point.t;
  const identity = (time) => time;

  function clock(time) {
    return new Date(time + offset).toISOString().replace("T", " ");
  }

  function fullTime(time, intraday) {
    return clock(time).slice(0, intraday ? 19 : 10);
  }

  function valueText(value, unit) {
    const number = Math.abs(value);
    const label = number && (number < 1e-6 || number >= 1e13) ? value.toExponential(5) : numbers.format(value);
    return label + (unit === "%" ? "%" : unit ? " " + unit : "");
  }

  function timeTicks(start, end, count, intraday) {
    const span = end - start;
    if (!span) return [{ time: start, label: intraday ? clock(start).slice(11, 19) : fullTime(start, false) }];
    const desired = span / count;
    const ticks = [];
    if (desired >= 28 * day) {
      const months = [1, 2, 3, 6, 12, 24, 36, 60, 120, 240, 600, 1200, 2400, 6000, 12000];
      const step = months.find((n) => n * 30.44 * day >= desired) ?? 24000;
      const cursor = new Date(start + offset);
      const month = cursor.getUTCFullYear() * 12 + cursor.getUTCMonth();
      cursor.setUTCDate(1);
      cursor.setUTCHours(0, 0, 0, 0);
      for (let n = Math.ceil(month / step) * step; ; n += step) {
        cursor.setUTCFullYear(Math.floor(n / 12), n % 12, 1);
        const time = cursor.getTime() - offset;
        if (time > end) break;
        if (time >= start) ticks.push({ time, label: clock(time).slice(0, step >= 12 ? 4 : 7) });
      }
    } else {
      const steps = [1, 2, 5, 10, 30, 100, 200, 500, 1000, 2000, 5000, 10000, 15000, 30000,
        60000, 120000, 300000, 600000, 900000, 1800000, hour, 2 * hour, 4 * hour,
        8 * hour, 12 * hour, day, 2 * day, 5 * day, 7 * day, 14 * day, 28 * day];
      const step = steps.find((n) => n >= desired);
      const format = span >= 3 * day ? (s) => s.slice(5, 10)
        : span >= day ? (s) => s.slice(5, 16)
        : step >= 60000 ? (s) => s.slice(11, 16)
        : step >= 1000 ? (s) => s.slice(11, 19)
        : (s) => s.slice(11, 23);
      for (let time = Math.ceil((start + offset) / step) * step - offset; time <= end; time += step) {
        ticks.push({ time, label: format(clock(time)) });
      }
    }
    return ticks;
  }

  function valueTicks(min, max) {
    const target = (max - min) / 5;
    const magnitude = 10 ** Math.floor(Math.log10(target));
    const step = [1, 2, 2.5, 5, 10].map((n) => n * magnitude).find((n) => n >= target);
    const first = Math.ceil(min / step);
    const ticks = [];
    for (let i = 0; i <= 6 && (first + i) * step <= max; i++) ticks.push((first + i) * step);
    return ticks;
  }

  function marker(ctx, x, y, shape, color, radius) {
    ctx.beginPath();
    if (shape === 1) {
      ctx.moveTo(x, y - radius - 1);
      ctx.lineTo(x + radius, y + radius);
      ctx.lineTo(x - radius, y + radius);
    } else if (shape === 2) {
      ctx.moveTo(x, y - radius - 1);
      ctx.lineTo(x + radius + 1, y);
      ctx.lineTo(x, y + radius + 1);
      ctx.lineTo(x - radius - 1, y);
    } else if (shape === 3) {
      ctx.rect(x - radius, y - radius, 2 * radius, 2 * radius);
    } else {
      ctx.arc(x, y, radius, 0, 2 * Math.PI);
    }
    ctx.closePath();
    ctx.fillStyle = color;
    ctx.fill();
  }

  function mount(section) {
    const data = JSON.parse(section.querySelector('script[type="application/json"]').textContent);
    const canvas = section.querySelector("canvas");
    const ctx = canvas.getContext("2d");
    const status = section.querySelector(".chart-status");
    if (!ctx) {
      status.className = "empty";
      status.textContent = "当前浏览器无法绘制图表。";
      return;
    }

    const plot = section.querySelector(".chart-plot");
    const tooltip = section.querySelector(".chart-tooltip");
    const cursor = section.querySelector(".chart-cursor");
    const selection = section.querySelector(".chart-selection");
    const windowLabel = section.querySelector(".time-window");
    const actions = section.querySelector(".chart-actions");
    const controls = section.querySelector(".chart-controls");
    const period = actions.querySelector(".period");
    const periodSelect = period.querySelector("select");
    const pan = section.querySelector(".pan");
    const slider = pan.querySelector("input");
    const legend = section.querySelector(".chart-legend");
    const zoomIn = actions.querySelector('[data-action="in"]');
    const zoomOut = actions.querySelector('[data-action="out"]');
    const reset = actions.querySelector('[data-action="reset"]');

    const series = data.series.map((line, index) => ({
      ...line, color: colors[index % colors.length], shape: index % shapes.length,
      dashed: index % 2 === 1, visible: true,
    }));
    const periods = [...new Set(series.map((line) => line.period ?? ""))];
    const legendItems = new Map();
    const labelFor = (line) => periodSelect.value === "" && periods.length > 1 &&
      !line.name.startsWith((line.period ?? "") + " ")
      ? line.name + " · " + (line.period || "未标注区间") : line.name;
    const swatchColor = (line, point) => !data.signedTrades ? line.color
      : point.v > 0 ? "#b32634" : point.v < 0 ? "#137143" : muted;
    let first, last, span, minWidth, intraday;
    let start, end, geometry, visibleTimes = [];
    let selectedTime, drag, scoped = [];

    for (const line of series) {
      const label = document.createElement("label");
      const input = document.createElement("input");
      const swatch = document.createElement("span");
      input.type = "checkbox";
      input.checked = true;
      input.addEventListener("change", () => {
        line.visible = input.checked;
        render();
      });
      swatch.className = "swatch" + (line.scatter ? " marker" : line.dashed ? " dashed" : "");
      swatch.style.setProperty("--series", data.signedTrades ? muted : line.color);
      swatch.setAttribute("aria-hidden", "true");
      if (line.scatter) swatch.textContent = shapes[line.shape];
      const text = document.createTextNode(line.name);
      label.append(input, swatch, text);
      legend.append(label);
      legendItems.set(line, { label, text });
    }

    if (periods.length > 1) {
      period.hidden = false;
      periodSelect.append(new Option("全部区间", ""));
      periods.forEach((name, index) => periodSelect.append(new Option(name || "未标注区间", String(index))));
      periodSelect.value = String(periods.findIndex(Boolean));
      periodSelect.addEventListener("change", selectPeriod);
    }

    function selectPeriod() {
      const active = periods[periodSelect.value];
      scoped = active === undefined ? series : series.filter((line) => (line.period ?? "") === active);
      for (const [line, item] of legendItems) {
        item.label.hidden = !scoped.includes(line);
        item.text.data = labelFor(line);
      }
      first = Math.min(...scoped.map((line) => line.points[0].t));
      last = Math.max(...scoped.map((line) => line.points.at(-1).t));
      span = last - first;
      minWidth = Math.min(span, 10);
      intraday = scoped.some((line) => line.points.some((point) => (point.t + offset) % day !== 0));
      setWindow(first, span);
    }

    function clearCursor() {
      selectedTime = undefined;
      cursor.hidden = tooltip.hidden = true;
      status.textContent = "";
    }

    function render() {
      clearCursor();
      const { width, height } = canvas.getBoundingClientRect();
      if (!width || !height) return;
      const ratio = window.devicePixelRatio || 1;
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
      ctx.setTransform(canvas.width / width, 0, 0, canvas.height / height, 0, 0);
      ctx.font = "11px ui-monospace, monospace";
      const view = [];
      const times = new Set();
      let low = Infinity, high = -Infinity;
      const include = (value) => {
        low = Math.min(low, value);
        high = Math.max(high, value);
      };
      for (const line of scoped.filter((item) => item.visible)) {
        const points = line.points;
        const from = lowerBound(points, start, timestamp);
        const to = lowerBound(points, end + 1, timestamp);
        const visible = points.slice(from, to);
        for (const point of visible) {
          times.add(point.t);
          include(point.v);
        }
        // Use the actual line segments at viewport edges, including between observations.
        if (!line.scatter) {
          for (const [at, index] of [[start, from], [end, to]]) {
            const a = points[index - 1], b = points[index];
            if (a && b && a.t < at && at < b.t) {
              const point = { t: at, v: a.v + (b.v - a.v) * ((at - a.t) / (b.t - a.t)) };
              include(point.v);
              if (at === start) visible.unshift(point);
              else visible.push(point);
            }
          }
        }
        if (visible.length) view.push({ line, points: visible });
      }
      visibleTimes = [...times].sort((a, b) => a - b);
      const zoomed = end - start < span;
      windowLabel.textContent = fullTime(start, intraday) + (start === end ? "" : " — " + fullTime(end, intraday)) + "  UTC+8";
      zoomIn.disabled = end - start <= minWidth;
      zoomOut.disabled = reset.disabled = !zoomed;
      pan.hidden = !zoomed;
      slider.value = zoomed ? Math.round(1000 * (start - first) / (span - (end - start))) : 0;

      if (!view.length) {
        geometry = undefined;
        ctx.fillStyle = muted;
        ctx.font = "14px system-ui, sans-serif";
        ctx.textAlign = "center";
        const message = scoped.some((line) => line.visible) ? "当前区间没有观测值" : "勾选图例以显示数据";
        ctx.fillText(message, width / 2, height / 2);
        status.textContent = message;
        return;
      }

      if (data.zeroBaseline) include(0);
      const pad = Math.max((high - low) * .10, Math.abs(high) * .000001, 1e-12);
      low -= pad;
      high += pad;
      const ticks = valueTicks(low, high);
      const axisValue = (value) => valueText(value, data.unit);
      const left = Math.min(width / 3, Math.max(43, ...ticks.map((v) => ctx.measureText(axisValue(v)).width + 13)));
      const right = width - 13, top = 12, bottom = height - 30;
      const x = (time) => start === end ? (left + right) / 2 : left + (right - left) * (time - start) / (end - start);
      const y = (value) => bottom - (bottom - top) * (value - low) / (high - low);
      geometry = { left, right, top, bottom, x };

      ctx.fillStyle = muted;
      ctx.textAlign = "right";
      ctx.textBaseline = "middle";
      for (const value of ticks) {
        const at = y(value);
        lineAcross(at, "#e0e8f0");
        ctx.fillText(axisValue(value), left - 8, at, left - 9);
      }
      if (data.zeroBaseline) lineAcross(y(0), "#8297ab", [5, 4]);
      lineAcross(bottom, "#a6b7c9");
      ctx.textAlign = "center";
      const labelGap = width < 560 ? 102 : 128;
      let labelRight = 0;
      for (const tick of timeTicks(start, end, Math.max(2, Math.floor((right - left) / labelGap)), intraday)) {
        const at = x(tick.time);
        const halfLabel = ctx.measureText(tick.label).width / 2;
        const labelAt = clamp(at, halfLabel + 1, width - halfLabel - 1);
        if (labelAt - halfLabel < labelRight + 6) continue;
        ctx.strokeStyle = "#a6b7c9";
        ctx.beginPath();
        ctx.moveTo(at, bottom);
        ctx.lineTo(at, bottom + 4);
        ctx.stroke();
        ctx.fillText(tick.label, labelAt, bottom + 15);
        labelRight = labelAt + halfLabel;
      }

      ctx.save();
      ctx.beginPath();
      ctx.rect(left - 4, top - 4, right - left + 8, bottom - top + 4);
      ctx.clip();
      for (const { line, points } of view) {
        if (line.scatter || points.length === 1) {
          for (const point of points) marker(ctx, x(point.t), y(point.v), line.shape, swatchColor(line, point), 3.5);
        } else {
          ctx.beginPath();
          ctx.moveTo(x(points[0].t), y(points[0].v));
          for (const point of points.slice(1)) ctx.lineTo(x(point.t), y(point.v));
          ctx.strokeStyle = line.color;
          ctx.lineWidth = line.dashed ? 1.6 : 2;
          ctx.lineJoin = "round";
          ctx.setLineDash(line.dashed ? [6, 4] : []);
          ctx.stroke();
        }
      }
      ctx.restore();

      function lineAcross(at, color, dash = []) {
        ctx.beginPath();
        ctx.strokeStyle = color;
        ctx.setLineDash(dash);
        ctx.moveTo(left, at);
        ctx.lineTo(right, at);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }

    function readTime(time, announce = false) {
      if (!geometry || time === undefined) return;
      selectedTime = time;
      const heading = document.createElement("strong");
      heading.textContent = fullTime(time, intraday);
      tooltip.replaceChildren(heading);
      const lines = [];
      for (const line of scoped.filter((item) => item.visible)) {
        const points = line.points;
        for (let i = lowerBound(points, time, timestamp); points[i]?.t === time; i++) {
          const point = points[i];
          const row = document.createElement("div");
          const value = document.createElement("b");
          value.style.color = swatchColor(line, point);
          value.textContent = valueText(point.v, data.unit);
          row.append(labelFor(line) + "  ", value, point.label ? " · " + point.label : "");
          lines.push(row);
        }
      }
      tooltip.append(...lines);
      tooltip.hidden = cursor.hidden = false;
      const x = geometry.x(time);
      cursor.style.left = x + "px";
      const space = plot.clientWidth - tooltip.offsetWidth;
      tooltip.style.left = clamp(x > space - 12 ? x - tooltip.offsetWidth - 12 : x + 12, 0, space) + "px";
      if (announce) status.textContent = [heading.textContent, ...lines.map((row) => row.textContent)].join("，");
    }

    function setWindow(at, width) {
      width = Math.round(clamp(width, minWidth, span));
      start = Math.round(clamp(at, first, last - width));
      end = start + width;
      render();
    }

    function zoom(factor) {
      const width = end - start;
      const next = clamp(width * factor, minWidth, span);
      const anchor = selectedTime ?? (start + end) / 2;
      setWindow(anchor - (anchor - start) * next / width, next);
    }

    function pointerX(event) {
      return event.clientX - canvas.getBoundingClientRect().left;
    }

    function timeAt(x) {
      return start + (end - start) * clamp((x - geometry.left) / (geometry.right - geometry.left), 0, 1);
    }

    function readNear(x, announce) {
      const target = timeAt(x);
      const i = lowerBound(visibleTimes, target, identity);
      const a = visibleTimes[i - 1] ?? Infinity;
      const b = visibleTimes[i] ?? Infinity;
      readTime(Math.abs(a - target) < Math.abs(b - target) ? a : Number.isFinite(b) ? b : undefined, announce);
    }

    zoomIn.addEventListener("click", () => zoom(.5));
    zoomOut.addEventListener("click", () => zoom(2));
    reset.addEventListener("click", () => setWindow(first, span));
    slider.addEventListener("input", () => {
      const width = end - start;
      setWindow(first + Number(slider.value) / 1000 * (span - width), width);
    });
    canvas.addEventListener("pointerdown", (event) => {
      if (event.button !== 0 || !geometry) return;
      if (event.pointerType === "mouse" && span) {
        drag = clamp(pointerX(event), geometry.left, geometry.right);
        canvas.setPointerCapture(event.pointerId);
      }
    });
    canvas.addEventListener("pointermove", (event) => {
      if (!geometry || event.pointerType === "touch") return;
      const x = pointerX(event);
      if (drag !== undefined) {
        clearCursor();
        const at = clamp(x, geometry.left, geometry.right);
        selection.hidden = false;
        selection.style.left = Math.min(drag, at) + "px";
        selection.style.width = Math.abs(drag - at) + "px";
      } else if (x >= geometry.left && x <= geometry.right) {
        readNear(x, false);
      } else {
        clearCursor();
      }
    });
    canvas.addEventListener("pointerup", (event) => {
      const x = pointerX(event);
      selection.hidden = true;
      const from = drag;
      drag = undefined;
      if (!geometry) return;
      if (from !== undefined && Math.abs(x - from) >= 8) {
        const left = timeAt(Math.min(x, from)), right = timeAt(Math.max(x, from));
        setWindow(left, right - left);
      } else {
        readNear(x, true);
      }
    });
    canvas.addEventListener("pointercancel", () => { drag = undefined; selection.hidden = true; });
    canvas.addEventListener("pointerleave", (event) => {
      if (event.pointerType !== "touch" && drag === undefined) clearCursor();
    });
    canvas.addEventListener("blur", clearCursor);
    canvas.addEventListener("keydown", (event) => {
      const key = event.key;
      if (key === "Escape") {
        clearCursor();
      } else if (key === "+" || key === "=" || key === "-") {
        if (key === "-" ? !zoomOut.disabled : !zoomIn.disabled) zoom(key === "-" ? 2 : .5);
      } else if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(key) && visibleTimes.length) {
        let index = lowerBound(visibleTimes, selectedTime ?? visibleTimes[0], identity);
        if (key === "Home") index = 0;
        if (key === "End") index = visibleTimes.length - 1;
        if (selectedTime !== undefined && key === "ArrowLeft") index--;
        if (selectedTime !== undefined && key === "ArrowRight") index++;
        readTime(visibleTimes[clamp(index, 0, visibleTimes.length - 1)], true);
      } else {
        return;
      }
      event.preventDefault();
    });

    controls.hidden = actions.hidden = false;
    selectPeriod();
    new ResizeObserver(render).observe(canvas);
  }

  document.querySelectorAll(".chart").forEach(mount);
})();
