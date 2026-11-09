// discovery.js 只处理统一作者视频页与导入任务，不解释平台发现方式。
// 翻页保留选择，添加全部由服务器枚举，加载失败不会叠加自动重试。
(function () {
  "use strict";
  class Selection {
    constructor() { this.items = new Map(); this.selected = new Set(); }
    merge(items) { for (const item of items) this.items.set(item.id, item); }
    toggle(id, value) { if (value) this.selected.add(id); else this.selected.delete(id); }
    rows(query) { const q = query.trim().toLocaleLowerCase(); return [...this.items.values()].filter(item => !q || `${item.id} ${item.title}`.toLocaleLowerCase().includes(q)); }
    imported(results) { for (const item of results) if (item.status === "added" || item.status === "existing") { this.items.delete(item.id); this.selected.delete(item.id); } }
  }
  if (typeof module !== "undefined") module.exports = { Selection };
  if (typeof document === "undefined") return;

  // 统一添加入口：BV/视频链接直接添加；UID/空间链接进入作者视频列表。
  function parseTarget(value) {
    const text = (value || "").trim();
    if (!text) return null;
    const bv = text.match(/BV[0-9A-Za-z]{10}/);
    if (bv) return { type: "video", id: bv[0] };
    const uid = text.match(/(?:space\.bilibili\.com\/)?(\d{1,19})/);
    if (uid) return { type: "creator", id: uid[1] };
    return null;
  }
  document.querySelectorAll("[data-quick-add]").forEach(form => {
    const hint = form.parentElement.querySelector("[data-quick-hint]");
    form.addEventListener("submit", event => {
      event.preventDefault();
      const input = form.querySelector('input[name="target"]');
      const target = parseTarget(input.value);
      if (!target) {
        if (hint) hint.textContent = "只支持 BV 号、UP 主 UID 或对应的 B站链接。";
        return;
      }
      if (target.type === "creator") {
        location.assign(`/manage?creator=${encodeURIComponent(target.id)}`);
        return;
      }
      const interval = form.querySelector("[data-quick-interval]");
      const body = new URLSearchParams({ type: "video", id: target.id, interval_hours: (interval && interval.value) || "3", return_to: "/manage" });
      if (hint) hint.textContent = "正在添加视频…";
      fetch("/targets", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "text/html" }, body })
        .then(() => location.assign(`/manage?message=${encodeURIComponent(`已添加视频，正在采集：${target.id}`)}`))
        .catch(() => { if (hint) hint.textContent = "添加失败，请稍后重试。"; });
    });
  });

  document.querySelectorAll("[data-creator-videos]").forEach(root => {
    const creator = root.dataset.creator;
    if (!creator) return;
    const get = name => root.querySelector(`[data-${name}]`), model = new Selection();
    const endpoint = `/api/creators/${encodeURIComponent(creator)}/videos`;
    let cursor = "", session = "", state = "pending", job = "", loading = false, running = false, timer, jobTimer, stopped = false;
    const text = (tag, value) => { const el = document.createElement(tag); el.textContent = value; return el; };
    async function json(url, body) {
      const response = await fetch(url, body === undefined ? { headers: { Accept: "application/json" } } : { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body) });
      const result = await response.json(); if (!response.ok) throw new Error(result.error || "请求失败，请稍后重试。"); return result;
    }
    function buttons() {
      get("add-selected").textContent = `添加已选（${model.selected.size}）`;
      get("add-selected").disabled = running || !model.selected.size;
      get("add-all").disabled = running || !session;
      get("video-more").disabled = loading || !cursor || state === "partial" || stopped;
      get("video-cancel").disabled = stopped || state !== "pending";
    }
    function render() {
      const rows = get("video-rows"); rows.replaceChildren();
      for (const item of model.rows(get("video-search").value)) {
        const row = document.createElement("tr"), cell = document.createElement("td"), checkbox = document.createElement("input");
        checkbox.type = "checkbox"; checkbox.checked = model.selected.has(item.id); checkbox.setAttribute("aria-label", `选择 ${item.title || item.id}`);
        checkbox.addEventListener("change", () => { model.toggle(item.id, checkbox.checked); buttons(); }); cell.append(checkbox);
        const video = document.createElement("td"), media = document.createElement("div"); media.className = "picker-video";
        if (item.cover_url && /^https?:\/\//.test(item.cover_url)) { const image = document.createElement("img"); image.src = item.cover_url; image.alt = ""; image.loading = "lazy"; image.referrerPolicy = "no-referrer"; image.addEventListener("error", () => image.remove()); media.append(image); }
        const label = document.createElement("div"); label.append(text("strong", item.title || item.id), text("small", `${item.id}${item.published_at ? " · " + new Date(item.published_at).toLocaleDateString("zh-CN", { timeZone: "Asia/Shanghai" }) : ""}`)); media.append(label); video.append(media);
        const record = text("td", item.recorded ? `${item.versions} 个版本${item.observed_at ? " · " + new Date(item.observed_at).toLocaleDateString("zh-CN", { timeZone: "Asia/Shanghai" }) : ""}` : "未采集");
        row.append(cell, video, record, text("td", item.view_count == null ? "未采集" : String(item.view_count))); rows.append(row);
      }
      if (!rows.children.length) { const row = document.createElement("tr"), cell = text("td", get("video-search").value ? "本次已发现的视频中没有匹配项。" : state === "complete" ? "暂无可添加视频。" : "正在等待可添加视频。"); cell.colSpan = 4; row.append(cell); rows.append(row); }
      buttons();
    }
    async function load(retry = false) {
      if (loading || stopped) return;
      loading = true; clearTimeout(timer); buttons();
      try {
        const params = new URLSearchParams({ platform: root.dataset.platform, scope: "available", limit: "30" });
        if (cursor) params.set("cursor", cursor); if (retry) params.set("retry", "1");
        const page = await json(`${endpoint}?${params}`); model.merge(page.items); cursor = page.next_cursor || ""; session = page.session; state = page.state;
        get("video-state").textContent = state === "partial" ? "视频列表加载不完整，已保留当前结果。可稍后重试或手动添加。" : state === "complete" ? "已读取服务器本次可获取的视频；这不保证覆盖作者的全部投稿。" : "正在发现更多视频，已有结果可以选择。";
        get("video-retry").hidden = state !== "partial"; get("video-retry").disabled = page.retry_at && new Date(page.retry_at).getTime() > Date.now();
        if (get("video-retry").disabled) { const delay = Math.min(new Date(page.retry_at).getTime() - Date.now(), 2147483647); setTimeout(() => { get("video-retry").disabled = false; }, delay); }
        render();
        // 仅轮询尚在执行的发现；partial 停止，用户显式重试后才再请求上游。
        if (state === "pending" && page.items.length === 0) timer = setTimeout(() => load(), 700);
      } catch (error) { get("video-state").textContent = error.message; get("video-retry").hidden = false; get("video-retry").disabled = false; }
      finally { loading = false; buttons(); }
    }
    function showProgress(progress) {
      job = progress.id; running = progress.state === "running";
      const box = get("import-progress"); box.hidden = false; box.className = `import-result ${progress.state}`;
      const titles = { running: "正在添加", complete: "添加完成", partial: "部分完成", cancelled: "已停止" };
      get("import-title").textContent = titles[progress.state] || progress.state;
      get("import-icon").textContent = progress.state === "complete" ? "✓" : progress.state === "partial" ? "!" : progress.state === "cancelled" ? "–" : "…";
      const processed = progress.added + progress.existing + progress.rejected + progress.failed;
      get("import-state").textContent = processed ? `已处理 ${processed} 个视频` : "正在准备…";
      get("import-added").textContent = progress.added;
      get("import-existing").textContent = progress.existing;
      get("import-rejected").textContent = progress.rejected;
      get("import-failed").textContent = progress.failed;
      const failures = progress.results.filter(item => item.status === "failed" || item.status === "rejected");
      const list = get("import-failures"); list.replaceChildren();
      for (const item of failures) list.append(text("li", `${item.id}：${item.reason === "creator_mismatch" ? "不属于当前作者" : "采集或保存失败"}`));
      get("import-failures-wrap").hidden = !failures.length;
      get("import-retry").hidden = progress.state !== "partial";
      get("import-cancel").hidden = !running;
      get("import-done").hidden = running;
      model.imported(progress.results); render();
      clearTimeout(jobTimer); if (running) jobTimer = setTimeout(pollJob, 700);
      buttons();
    }
    async function pollJob() { try { showProgress(await json(`/api/ingest/jobs/${encodeURIComponent(job)}`)); } catch (error) { running = false; get("import-state").textContent = `${error.message} 可点击“恢复进度查询”。`; get("import-retry").hidden = false; get("import-retry").textContent = "恢复进度查询"; buttons(); } }
    async function start(all) {
      if (running || !session) return;
      if (!all && model.selected.size > 100) { get("video-state").textContent = "每次最多选择 100 个视频，也可以使用一键添加全部。"; return; }
      running = true; buttons();
      try { const body = { platform: root.dataset.platform, creator_id: creator, all, session, videos: all ? [] : [...model.selected] }; showProgress(await json("/api/ingest/videos", body)); }
      catch (error) { get("video-state").textContent = error.message; running = false; buttons(); }
    }
    get("video-search").addEventListener("input", render);
    get("video-more").addEventListener("click", () => load()); get("video-retry").addEventListener("click", () => load(true));
    get("add-selected").addEventListener("click", () => start(false)); get("add-all").addEventListener("click", () => start(true));
    get("video-cancel").addEventListener("click", async () => { stopped = true; clearTimeout(timer); try { await json(`${endpoint}/cancel`, { session }); get("video-state").textContent = "已停止发现，当前结果和选择已保留。"; } catch (error) { get("video-state").textContent = error.message; } buttons(); });
    get("import-retry").addEventListener("click", async () => { try { if (get("import-retry").textContent === "恢复进度查询") { get("import-retry").textContent = "重试失败及未完成项"; await pollJob(); } else showProgress(await json(`/api/ingest/jobs/${encodeURIComponent(job)}/retry`, {})); } catch (error) { get("import-state").textContent = error.message; } });
    get("import-cancel").addEventListener("click", async () => { try { showProgress(await json(`/api/ingest/jobs/${encodeURIComponent(job)}/cancel`, {})); } catch (error) { get("import-state").textContent = error.message; } });
    get("import-done").addEventListener("click", () => { get("import-progress").hidden = true; root.scrollIntoView({ behavior: "smooth", block: "start" }); });
    window.addEventListener("pagehide", () => { clearTimeout(timer); clearTimeout(jobTimer); });
    load();
  });
})();
