/* 原生 SVG 趋势图与渐进增强交互：保留真实缺口、空状态、键盘访问和无 JS 表单。 */
(() => {
  "use strict";
  const DAY = 86400000;
  function segments(points) {
    const result = [];
    for (const point of points) {
      const previous = result.at(-1);
      if (!previous || point.at - previous.at(-1).at > DAY) result.push([point]);
      else previous.push(point);
    }
    return result;
  }
  function summary(points) {
    if (!points.length) return { count: 0, delta: null, from: null, to: null };
    return { count: points.length, delta: points.length > 1 ? points.at(-1).value - points[0].value : null, from: points[0].at, to: points.at(-1).at };
  }
  if (typeof module !== "undefined") module.exports = { segments, summary };
  if (typeof document === "undefined") return;
  document.documentElement.classList.add("js");
  const number = value => new Intl.NumberFormat("zh-CN").format(value);
  const compact = value => new Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 1 }).format(value);
  const utcDate = at => new Date(at).toISOString().slice(0, 10);
  const svgNS = "http://www.w3.org/2000/svg";
  let chartID = 0;
  let toastTimer;
  function toast(message, error = false) {
    const el = document.getElementById("toast");
    el.textContent = message; el.hidden = false;
    el.toggleAttribute("data-error", error);
    clearTimeout(toastTimer); toastTimer = setTimeout(() => { el.hidden = true; }, 5500);
  }
  function svgNode(tag, attrs = {}, text) {
    const node = document.createElementNS(svgNS, tag);
    for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, String(value));
    if (text !== undefined) node.textContent = text;
    return node;
  }
  function renderChart(container) {
    let series;
    try { series = JSON.parse(container.dataset.series); } catch { series = { points: [] }; }
    const points = (series.points || []).filter(p => Number.isFinite(p.at) && Number.isFinite(p.value));
    container.replaceChildren();
    const stats = summary(points);
    const panel = container.closest("[data-metric-panel]");
    const caption = panel?.querySelector("[data-chart-summary]");
    if (caption) caption.textContent = !stats.count ? "暂无有效日采样，成功采集后会开始积累。" : `${stats.count} 个真实采样 · ${utcDate(stats.from)} 至 ${utcDate(stats.to)}${stats.delta === null ? " · 单点不足以计算增长" : ` · 净变化 ${stats.delta > 0 ? "+" : ""}${number(stats.delta)}`}${series.truncated ? " · 仅显示最近 5000 点" : ""}`;
    if (!points.length) {
      const empty = document.createElement("div"); empty.className = "chart-fallback";
      const title = document.createElement("strong"); title.textContent = "等待第一份日采样";
      const text = document.createElement("span"); text.textContent = "历史从首次采集开始，不补造之前的数据。";
      empty.append(title, text); container.append(empty); return;
    }
    const width = Math.max(240, container.clientWidth), height = container.clientHeight || 240;
    const left = 52, right = 17, top = 20, bottom = 30;
    const plotWidth = width - left - right, plotHeight = height - top - bottom;
    let low = Math.min(...points.map(p => p.value)), high = Math.max(...points.map(p => p.value));
    const span = Math.max(high - low, Math.abs(high) * 0.04, 1);
    low = Math.max(0, low - span * 0.15); high += span * 0.2;
    if (high-low<4) {low=Math.max(0,Math.floor(Math.min(...points.map(p=>p.value)))-2);high=low+4;}
    const from = stats.from, to = stats.to;
    const x = p => points.length === 1 ? left + plotWidth / 2 : left + (p.at - from) / (to - from) * plotWidth;
    const y = value => top + plotHeight - (value - low) / (high - low) * plotHeight;
    const svg = svgNode("svg", { viewBox: `0 0 ${width} ${height}`, role: "img", "aria-label": `${container.dataset.label}，${stats.count} 个真实采样，UTC 日期`, tabindex: "0" });
    const title = svgNode("title", {}, stats.delta === null ? "只有一个有效采样点，暂无趋势。" : `采样期间净变化 ${number(stats.delta)}；缺失日没有连线。`); svg.append(title);
    for (let tick = 0; tick <= 4; tick++) {
      const value = low + (high - low) * tick / 4, py = y(value);
      svg.append(svgNode("line", { x1: left, x2: width - right, y1: py, y2: py, stroke: "#edf1f7", "stroke-dasharray": "4 5" }));
      svg.append(svgNode("text", { x: left - 11, y: py + 3, "text-anchor": "end", fill: "#91a0b8", "font-size": 9 }, compact(Math.round(value))));
    }
    const ticks = points.length === 1 ? [points[0]] : Array.from(new Set([0, Math.round((points.length - 1) / 3), Math.round((points.length - 1) * 2 / 3), points.length - 1])).map(index => points[index]);
    ticks.forEach(p => svg.append(svgNode("text", { x: x(p), y: height - 7, "text-anchor": "middle", fill: "#91a0b8", "font-size": 9 }, utcDate(p.at).slice(5))));
    const gradientID = `chart-fill-${++chartID}`;
    const defs = svgNode("defs"), gradient = svgNode("linearGradient", { id: gradientID, x1: "0", y1: "0", x2: "0", y2: "1" });
    gradient.append(svgNode("stop", { offset: "0%", "stop-color": "#91adf4", "stop-opacity": ".24" }), svgNode("stop", { offset: "100%", "stop-color": "#91adf4", "stop-opacity": ".015" })); defs.append(gradient); svg.append(defs);
    for (const segment of segments(points)) {
      const coords = segment.map(p => `${x(p)},${y(p.value)}`).join(" ");
      if (segment.length > 1) {
        svg.append(svgNode("polygon", { points: `${x(segment[0])},${top + plotHeight} ${coords} ${x(segment.at(-1))},${top + plotHeight}`, fill: `url(#${gradientID})` }));
        svg.append(svgNode("polyline", { points: coords, fill: "none", stroke: "#6d91e9", "stroke-width": 2.2, "stroke-linecap": "round", "stroke-linejoin": "round" }));
      }
      if (points.length <= 90 || segment.length === 1) segment.forEach(p => svg.append(svgNode("circle", { cx: x(p), cy: y(p.value), r: segment.length === 1 ? 4 : 2.7, fill: "#7b9ce9", stroke: "white", "stroke-width": 2 })));
    }
    const guide = svgNode("line", { x1: 0, x2: 0, y1: top, y2: top + plotHeight, stroke: "#b8c8ec", "stroke-dasharray": "3 4", visibility: "hidden" });
    const focus = svgNode("circle", { cx: 0, cy: 0, r: 4.5, fill: "#6d91e9", stroke: "white", "stroke-width": 2, visibility: "hidden" }); svg.append(guide, focus);
    const tooltip = document.createElement("div"); tooltip.className = "chart-tooltip"; tooltip.hidden = true;
    let active = 0;
    function show(index) {
      active = Math.max(0, Math.min(points.length - 1, index)); const point = points[active];
      guide.setAttribute("x1", x(point)); guide.setAttribute("x2", x(point)); guide.setAttribute("visibility", "visible"); focus.setAttribute("cx", x(point)); focus.setAttribute("cy", y(point.value)); focus.setAttribute("visibility", "visible");
      const label = document.createElement("span"), value = document.createElement("strong"); label.textContent = `${utcDate(point.at)} · UTC`; value.textContent = number(point.value); tooltip.replaceChildren(label, value); tooltip.hidden = false;
      tooltip.style.left = `${Math.max(0, Math.min(width - 145, x(point) + 12))}px`; tooltip.style.top = `${Math.max(3, y(point.value) - 60)}px`;
    }
    svg.addEventListener("pointermove", event => { const position = (event.clientX - svg.getBoundingClientRect().left) / svg.getBoundingClientRect().width * width; const target = from + (position - left) / plotWidth * (to - from); let l = 0, r = points.length - 1; while (l < r) { const m = Math.floor((l + r) / 2); if (points[m].at < target) l = m + 1; else r = m; } const index = l > 0 && Math.abs(points[l - 1].at - target) < Math.abs(points[l].at - target) ? l - 1 : l; show(index); });
    const hide = () => { tooltip.hidden = true; guide.setAttribute("visibility", "hidden"); focus.setAttribute("visibility", "hidden"); };
    svg.addEventListener("pointerleave", hide); svg.addEventListener("blur", hide); svg.addEventListener("focus", () => show(active));
    svg.addEventListener("keydown", event => { if (event.key === "ArrowLeft" || event.key === "ArrowRight") { event.preventDefault(); show(active + (event.key === "ArrowLeft" ? -1 : 1)); } });
    container.append(svg, tooltip);
  }
  document.querySelectorAll("[data-chart]").forEach(container => { renderChart(container); let timer; new ResizeObserver(() => { clearTimeout(timer); timer = setTimeout(() => renderChart(container), 80); }).observe(container); });
  const selector = document.querySelector("[data-metric-days]");
  if (selector) {
    let controller, previous = selector.value;
    selector.closest("form").addEventListener("submit", event => event.preventDefault());
    selector.addEventListener("change", async () => {
      controller?.abort(); controller = new AbortController(); const panel = selector.closest("[data-metric-panel]"), chart = panel.querySelector("[data-chart]"); panel.setAttribute("aria-busy", "true");
      try {
        const response = await fetch(`${chart.dataset.endpoint}?days=${encodeURIComponent(selector.value)}`, { signal: controller.signal, headers: { Accept: "application/json" } }); if (!response.ok) throw new Error("无法读取趋势，请稍后重试。");
        const series = await response.json(); chart.dataset.series = JSON.stringify(series); renderChart(chart);
        const rows = panel.querySelector("[data-metric-rows]"); rows.replaceChildren();
        for (const point of series.points) { const row = document.createElement("tr"), at = document.createElement("td"), value = document.createElement("td"); at.textContent = utcDate(point.at); value.textContent = number(point.value); row.append(at, value); rows.append(row); }
        if (!series.points.length) { const row = document.createElement("tr"), cell = document.createElement("td"); cell.colSpan = 2; cell.textContent = "暂无有效日采样。"; row.append(cell); rows.append(row); }
        const url = new URL(location.href); url.searchParams.set("days", selector.value); history.replaceState(null, "", url); previous = selector.value;
      } catch (error) { if (error.name !== "AbortError") { selector.value = previous; toast(error.message, true); } } finally { panel.removeAttribute("aria-busy"); }
    });
  }
  document.querySelectorAll("img").forEach(image => { image.addEventListener("error", () => { image.style.display = "none"; }); if (image.complete && image.naturalWidth === 0) image.style.display = "none"; });
  const menu = document.querySelector("[data-menu]"), nav = document.getElementById("navigation");
  function closeMenu() { nav.classList.remove("open"); menu?.setAttribute("aria-expanded", "false"); }
  menu?.addEventListener("click", () => { const open = nav.classList.toggle("open"); menu.setAttribute("aria-expanded", String(open)); });
  document.addEventListener("keydown", event => { if (event.key === "Escape") closeMenu(); }); document.addEventListener("click", event => { if (!nav.contains(event.target) && !menu?.contains(event.target)) closeMenu(); });
  document.querySelectorAll("form[data-confirm]").forEach(form => form.addEventListener("submit", event => { if (!window.confirm(form.dataset.confirm)) event.preventDefault(); }));
  document.querySelectorAll("form[data-async]").forEach(form => form.addEventListener("submit", async event => {
    event.preventDefault(); const button = form.querySelector("button[type=submit],button:not([type])"), oldText = button.textContent; button.disabled = true; button.textContent = "提交中…"; form.setAttribute("aria-busy", "true");
    try {
      const response = await fetch(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)), headers: { Accept: "application/json" } }); const result = await response.json(); if (!response.ok || result.error) throw new Error(result.error || "提交失败，请稍后重试。");
      toast(result.message); setTimeout(() => location.assign(result.redirect), 500);
    } catch (error) { toast(error.message || "请求失败，请检查连接。", true); button.disabled = false; button.textContent = oldText; form.removeAttribute("aria-busy"); }
  }));
  if (document.querySelector("[data-poll-status]")) {
    let attempts = 0;
    const timer = setInterval(async () => {
      if (document.hidden) return; if (++attempts > 12) { clearInterval(timer); return; }
      try {
        const response = await fetch(`/api/targets/status${location.search}`, { headers: { Accept: "application/json" } }); if (!response.ok) return;
        const statuses = await response.json(), rows = new Map([...document.querySelectorAll("[data-state-key]")].map(row => [row.dataset.stateKey, row]));
        for (const item of statuses) {
          const row = rows.get(item.Key); if (!row) continue;
          row.querySelector(".target-name").textContent = item.Name;
          const state = row.querySelector("[data-status]"); state.className = `status status-${item.State}`; state.replaceChildren(document.createElement("i"), document.createTextNode(item.Label));
          row.querySelector("[data-last]").textContent = item.Last; row.querySelector("[data-next]").textContent = item.Next; row.querySelector("[data-reason]").textContent = item.State === "error" ? item.Reason : "";
          const refresh = row.querySelector("form[data-async] button"); if (refresh) refresh.disabled = item.State === "running";
        }
      } catch { /* 短暂断连保留已有内容；下一次轮询恢复，不触发任何采集。 */ }
    }, 5000);
  }
})();
