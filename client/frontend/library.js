// library.js 管理本地关注、服务器连接和资料弹窗；文本仅以 textContent 呈现。
// 所有远程访问经 Agent，关注保存与公共提交分别确认，见 docs/client-library.md。
export function initLibrary({api, notify, onScan}) {
  const $ = id => document.getElementById(id);
  let usable = false, busy = false, server = '', kind = 'creator', page = 1, result = {items:[]};
  let active = null, detailPage = 1, detailTotal = 0, detailLimit = 10, detailSequence = 0;
  const number = value => value == null ? '未知' : Number(value).toLocaleString('zh-CN');
  const date = value => value ? new Date(value).toLocaleString('zh-CN', {timeZone:'Asia/Shanghai'}) : '尚无观测';
  const path = f => `/api/follows/${encodeURIComponent(f.kind)}/${encodeURIComponent(f.id)}`;
  const post = url => api(url, {method:'POST',body:'{}'});
  function node(tag, text, className) {
    const element = document.createElement(tag);
    if (text != null) element.textContent = text;
    if (className) element.className = className;
    return element;
  }
  function button(text, action, className = 'button secondary') {
    const element = node('button',text,className); element.type = 'button'; element.disabled = !usable || busy;
    element.addEventListener('click', () => run(action)); return element;
  }
  function controls() {
    for (const el of $('library-panel').querySelectorAll('input,select,button')) el.disabled = !usable || busy;
    $('library-previous').disabled = !usable || busy || result.page <= 1;
    $('library-next').disabled = !usable || busy || result.page * result.limit >= result.total;
    $('refresh-library').disabled = !usable || busy || !server || !result.items.length;
    $('test-server').disabled = !usable || busy || !server;
    for (const el of $('library-panel').querySelectorAll('[data-library-kind]')) el.setAttribute('aria-pressed', String(el.dataset.libraryKind === kind));
    $('detail-previous').disabled = busy || detailPage <= 1;
    $('detail-next').disabled = busy || detailPage * detailLimit >= detailTotal;
    for (const el of $('library-detail').querySelectorAll('.version-button')) el.disabled = !usable || busy;
  }
  async function load() {
    const query = new URLSearchParams({kind, page:String(page), q:$('library-query').value.trim()});
    result = await api(`/api/follows?${query}`); page = result.page;
    $('creator-library-count').textContent = result.creators;
    $('video-library-count').textContent = result.videos;
    $('library-page').textContent = `${page} / ${Math.max(1, Math.ceil(result.total / result.limit))} · ${result.total} 项`;
    const rows = document.createDocumentFragment();
    for (const f of result.items) {
      const row = node('article',null,'follow-row');
      const info = node('div',null,'follow-info');
      info.append(node('strong',f.data?.item.name || f.id),node('span',`${f.kind === 'creator' ? 'UP · UID' : '视频'} ${f.id}`));
      const metric = f.kind === 'creator' ? '粉丝' : '播放';
      info.append(node('p',`${metric} ${number(f.data?.item.metric)} · 最近7天净变化 ${number(f.data?.item.growth)} · 版本 ${number(f.data?.item.seq)}`));
      const state = !f.remote ? '服务器状态未查询' : f.remote.tracking ? '服务器跟踪中' : f.remote.status === 'paused' ? '服务器已暂停' : f.remote.recorded ? '服务器已记录，未跟踪' : '服务器尚未记录';
      info.append(node('span',`已本地关注 · ${state}`, 'follow-status'));
      if (f.updated_at) info.append(node('small',`上次核对 ${date(f.updated_at)}${f.data ? ` · 数据观测 ${date(f.data.item.observed_at)}` : ''}`));
      if (f.last_error) info.append(node('p',`最近操作未完成：${f.last_error}`, 'follow-error'));
      const actions = node('div',null,'follow-actions');
      actions.append(button('刷新资料',async () => {await post(`${path(f)}/refresh`);await load();}),button('提交服务器',async () => {await submit(f);await load();}));
      if (f.data) actions.append(button('详情与历史',() => openDetail(f)));
      if (f.kind === 'creator') actions.append(button('获取全部视频',() => onScan(f.id)));
      actions.append(button('取消本地关注',async () => {await api(path(f),{method:'DELETE',body:'{}'});await load();notify('已取消本地关注，服务器跟踪与历史保留。');},'text-button danger'));
      row.append(info,actions);rows.append(row);
    }
    if (!result.items.length) rows.append(node('p',result.total ? '本页暂无结果。' : '暂无匹配的本地关注。添加 UID 或 BV 号即可开始，无需配置 B站 Cookie。','library-empty'));
    $('follow-rows').replaceChildren(rows); controls();
  }
  async function run(action) {
    if (!usable || busy) return;
    busy = true;controls();
    try {await action();} catch (error) {notify(error.message,true);await load().catch(() => {});}
    finally {busy = false;controls();}
  }
  async function submit(f) {
    await post(`${path(f)}/submit`);
    notify('服务器已确认跟踪；个人关注保存在本地。');
    await post(`${path(f)}/refresh`).catch(error => notify(`跟踪已确认，但读取资料失败：${error.message}`,true));
  }
  async function add(followKind,id) {
    const f = await api('/api/follows',{method:'POST',body:JSON.stringify({kind:followKind,id})});
    kind = followKind;page = 1;
    await load();notify('已保存本地关注。');
    if (server && $('submit-on-follow').checked) {
      try {await submit(f);} catch(error) {notify(`本地关注已保存；服务器尚未确认：${error.message}`,true);}
    } else if (server) {
      await post(`${path(f)}/refresh`).catch(error => notify(`已本地关注；读取服务器失败：${error.message}`,true));
    }
    await load();
  }
  async function readDetail(seq = 0) {
    const sequence = ++detailSequence;
    const query = new URLSearchParams({page:String(detailPage)});if (seq) query.set('seq',String(seq));
    const d = await api(`/api/library/${active.kind}/${encodeURIComponent(active.id)}?${query}`);
    if (sequence !== detailSequence || !$('library-detail').open) return;
    detailPage = d.version_page;detailTotal = d.version_total;detailLimit = d.version_limit;
    $('detail-title').textContent = d.item.name || active.id;
    $('detail-summary').textContent = `${active.id} · ${active.kind === 'creator' ? '粉丝' : '播放'} ${number(d.item.metric)} · 观测于 ${date(d.item.observed_at)}`;
    const versions = document.createDocumentFragment();
    for (const v of d.versions) versions.append(button(`版本 ${v.seq} · ${date(v.at)} · ${v.name}`,() => readDetail(v.seq),'version-button'));
    $('detail-versions').replaceChildren(versions);
    $('detail-version-page').textContent = `${detailPage} / ${Math.max(1,Math.ceil(detailTotal / detailLimit))} · ${detailTotal} 个版本`;
    $('detail-snapshot').textContent = d.snapshot ? JSON.stringify(d.snapshot,null,2) : '点击版本查看服务器保存的元数据快照。';
    const points = document.createDocumentFragment();
    for (const p of d.metrics.points) {const row = node('tr');row.append(node('td',date(p.at)),node('td',number(p.value)));points.append(row);}
    $('detail-metrics').replaceChildren(points);
    $('detail-metric-note').textContent = d.metrics.points.length ? `${d.metrics.points.length} 个真实日采样（最近30天）${d.metrics.truncated ? '，结果有截断' : ''}` : '暂无日采样，历史不补造。';
    controls();
  }
  async function openDetail(f) {
    active = f;detailPage = 1;detailTotal = 0;
    $('detail-title').textContent = '正在读取详情…';$('detail-summary').textContent = f.id;
    $('detail-versions').replaceChildren();$('detail-metrics').replaceChildren();$('detail-snapshot').textContent = '';
    $('library-detail').showModal();await readDetail();
  }
  $('server-form').addEventListener('submit',event => {event.preventDefault();run(async () => {
    const value = await api('/api/remote/connection',{method:'PUT',body:JSON.stringify({server_url:$('server-url').value.trim()})});
    server = value.server_url;$('server-url').value = server;
    $('server-note').textContent = server ? `当前服务器：${server}。资料按需读取，个人关注不上传为账户列表。` : '未配置服务器；本地关注可独立使用。';
    await load();notify('服务器配置已保存。');
  });});
  $('test-server').addEventListener('click',() => run(async () => {await post('/api/remote/test');notify('服务器连接成功，客户端 JSON 接口可用。');}));
  $('follow-form').addEventListener('submit',event => {event.preventDefault();run(async () => {await add($('follow-kind').value,$('follow-input').value.trim());$('follow-input').value = '';} );});
  for (const tab of $('library-panel').querySelectorAll('[data-library-kind]')) tab.addEventListener('click',() => run(async () => {kind = tab.dataset.libraryKind;page = 1;await load();}));
  $('library-search').addEventListener('submit',event => {event.preventDefault();run(async () => {page = 1;await load();});});
  $('library-previous').addEventListener('click',() => run(async () => {page--;await load();}));
  $('library-next').addEventListener('click',() => run(async () => {page++;await load();}));
  $('refresh-library').addEventListener('click',() => run(async () => {
    let failed = 0, completed = 0;
    for (const f of result.items) {
      try {await post(`${path(f)}/refresh`);} catch {failed++;break;}
      completed++;$('refresh-library').textContent = `已刷新 ${completed}/${result.items.length}`;
    }
    $('refresh-library').textContent = '刷新本页服务器资料';
    await load();notify(failed ? `已刷新 ${completed} 项，遇到失败后停止；原有缓存与关注保留，可逐项重试。` : '本页资料已刷新。',failed > 0);
  }));
  $('detail-close').addEventListener('click',() => {$('library-detail').close();detailSequence++;});
  $('library-detail').addEventListener('close',() => {detailSequence++;});
  $('detail-previous').addEventListener('click',() => run(async () => {detailPage--;await readDetail();}));
  $('detail-next').addEventListener('click',() => run(async () => {detailPage++;await readDetail();}));
  controls();
  return {
    setConnected(value) {usable = value;controls();},
    async connect() {const value = await api('/api/remote/connection');server = value.server_url;$('server-url').value = server;$('server-note').textContent = server ? `当前服务器：${server}。显示本地缓存，点击刷新获取最新数据。` : '未配置服务器；本地关注可独立使用。';await load();},
    add(followKind,id) {return run(() => add(followKind,id));}
  };
}
