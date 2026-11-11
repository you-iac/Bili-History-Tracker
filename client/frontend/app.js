// app.js 只向同源 Agent 发起请求；Cookie 和连接密钥不写入浏览器存储。
import {stateLabel, progress, publishedAt, rangeLabel} from './view.mjs';

const $ = id => document.getElementById(id);
let csrf = '', connected = false, online = false, config = null, job = null;
let offset = 0, busyAction = false, refreshSequence = 0, timer;
const pageSize = 50;

function notify(message, error = false) {
  $('notice').textContent = message;
  $('notice').className = `notice${error ? ' error' : ''}`;
  $('notice').hidden = !message;
}
function connection(text, isOnline) {
  online = isOnline;
  $('connection-status').textContent = text;
  $('connection-status').className = `connection-status${isOnline ? '' : ' offline'}`;
}
async function request(path, options = {}) {
  const method = options.method || 'GET';
  let response;
  try {
    response = await fetch(path, {credentials:'same-origin',cache:'no-store',signal:AbortSignal.timeout(25000),...options,
      headers:{...(method === 'GET' ? {} : {'Content-Type':'application/json','X-CSRF-Token':csrf}),...options.headers}});
  } catch {
    throw new Error('无法连接本地 Agent，请确认程序仍在运行。');
  }
  if (!response.ok) {
    const error = await response.json().catch(() => null);
    if (error?.error?.code === 'agent_unauthorized') {
      connected = false; csrf = ''; $('connect-card').hidden = false;
      connection('等待连接', false);
    }
    throw new Error(error?.error?.message || '操作未完成，请稍后重试。');
  }
  return response;
}
async function api(path, options) { return (await request(path, options)).json(); }
function buttons() {
  const usable = connected && online && !busyAction;
  const running = job?.state === 'running' || job?.state === 'cancelling';
  $('save-cookie').disabled = !usable || running;
  $('cookie-input').disabled = !usable || running;
  $('validate-cookie').disabled = !usable || running || !config?.configured;
  $('clear-cookie').disabled = !usable || running || !config?.configured;
  $('creator-input').disabled = !usable || running;
  $('start-scan').disabled = !usable || running || !config?.configured;
  $('start-scan').firstChild.textContent = job ? '重新扫描 ' : '开始扫描 ';
  $('resume-scan').hidden = !job?.can_resume;
  $('resume-scan').disabled = !usable || !config?.configured;
  $('cancel-scan').hidden = !running;
  $('cancel-scan').disabled = !usable || job?.state === 'cancelling';
  for (const id of ['copy-ids','export-txt','export-json']) $(id).disabled = !usable || !job?.count;
}
function renderConfig(value) {
  config = value;
  const account = config.account;
  $('account-name').textContent = account?.name || (config.configured ? '已保存 Cookie' : '尚未连接账号');
  $('account-id').textContent = account ? `UID ${account.id}` : (config.configured ? '开始扫描时会重新验证登录状态' : '配置 Cookie 后验证身份');
  $('account-avatar').textContent = account?.name?.slice(0,1) || 'B';
  $('account-check').hidden = !account;
  $('account-badge').textContent = account ? '已验证' : (config.configured ? '待验证' : '未配置');
  $('account-badge').className = `badge ${account ? 'success' : 'muted'}`;
  $('storage-note').textContent = config.persistent ? '由当前 Windows 账户加密保存，仅留在本机。' : '当前系统仅在本次进程中保留 Cookie，退出 Agent 后需重新配置。';
}
function renderResult(result) {
  job = result.job;
  offset = result.offset;
  const count = job?.count || 0;
  const total = job?.total;
  $('collected-count').replaceChildren(document.createTextNode(count.toLocaleString('zh-CN')), Object.assign(document.createElement('small'), {textContent:'条'}));
  $('reported-count').textContent = total == null ? '—' : total.toLocaleString('zh-CN');
  $('page-count').replaceChildren(document.createTextNode(String(job?.pages || 0)), Object.assign(document.createElement('small'), {textContent:'页'}));
  $('scan-badge').textContent = stateLabel(job?.state);
  $('scan-badge').className = `badge ${!job ? 'muted' : job.state === 'complete' ? 'success' : job.state === 'running' || job.state === 'cancelling' ? '' : 'warning'}`;
  $('scan-uid').textContent = job ? `UP 主 UID ${job.creator_id}` : '准备你的第一份视频清单';
  $('scan-message').textContent = job?.message || '配置账号并输入 UP 主 UID，即可开始。';
  $('scan-progress').value = progress(count,total,job?.state);
  $('results-count').textContent = count.toLocaleString('zh-CN');
  $('results-description').textContent = job ? `UP 主 ${job.creator_id} · ${stateLabel(job.state)}${job.state === 'complete' ? ' · 本次可见范围' : ' · 可导出当前已获取的结果'}` : '扫描结果将在这里显示。';
  $('empty-state').hidden = count > 0;
  $('table-container').hidden = !count;
  if (job && !count) {
    $('empty-state').querySelector('strong').textContent = job.state === 'complete' ? '该 UP 主当前没有可见投稿' : (job.state === 'running' ? '正在读取投稿列表…' : '尚未获取到视频 ID');
    $('empty-state').querySelector('p').textContent = job.message;
  }
  const rows = document.createDocumentFragment();
  result.videos.forEach((video,index) => {
    const row = document.createElement('tr');
    const number = document.createElement('td'); number.textContent = String(offset + index + 1);
    const id = document.createElement('td');
    const link = document.createElement('a'); link.textContent = video.id; link.href = `https://www.bilibili.com/video/${encodeURIComponent(video.id)}`; link.target = '_blank'; link.rel = 'noreferrer'; id.append(link);
    const title = document.createElement('td'); title.textContent = video.title || '未提供标题';
    const date = document.createElement('td'); date.textContent = publishedAt(video.published_at);
    row.append(number,id,title,date); rows.append(row);
  });
  $('video-rows').replaceChildren(rows);
  $('page-range').textContent = rangeLabel(offset,result.videos.length,count);
  $('page-number').textContent = String(Math.floor(offset / pageSize) + 1);
  $('previous-page').disabled = !online || offset === 0;
  $('next-page').disabled = !online || !result.has_more;
  if (job && !$('creator-input').value) $('creator-input').value = job.creator_id;
  buttons();
}
async function refresh() {
  clearTimeout(timer);
  if (!connected) return;
  const sequence = ++refreshSequence;
  try {
    const [configuration,result] = await Promise.all([api('/api/config'),api(`/api/scan?offset=${offset}&limit=${pageSize}`)]);
    if (sequence !== refreshSequence) return;
    connection('Agent 已连接', true);
    renderConfig(configuration); renderResult(result);
  } catch (error) {
    if (sequence !== refreshSequence) return;
    connection(connected ? 'Agent 连接中断' : '等待连接', false);
    notify(error.message,true); buttons();
  }
  if (sequence === refreshSequence && connected) timer = setTimeout(refresh, job?.state === 'running' || job?.state === 'cancelling' ? 1200 : 4000);
}
async function connect(token) {
  const result = await api('/api/session', token ? {method:'POST',body:JSON.stringify({token})} : undefined);
  csrf = result.csrf_token; connected = true; connection('Agent 已连接',true);
  $('connect-card').hidden = true; $('connect-token').value = ''; notify(''); await refresh();
}
async function action(work) {
  if (busyAction) return;
  busyAction = true; buttons(); notify('');
  try { await work(); await refresh(); } catch (error) { notify(error.message,true); }
  finally {busyAction = false; buttons();}
}
$('connect-form').addEventListener('submit', async event => {
  event.preventDefault();
  const button = event.currentTarget.querySelector('button'); button.disabled = true;
  try {await connect($('connect-token').value.trim());} catch(error) {notify(error.message,true);} finally {button.disabled = false;}
});
$('cookie-form').addEventListener('submit', event => {
  event.preventDefault();
  action(async () => {
    $('save-cookie').textContent = '正在验证登录状态…';
    try {
      await api('/api/config/cookie',{method:'PUT',body:JSON.stringify({cookie:$('cookie-input').value})});
      notify('账号验证成功，Cookie 已交给本地 Agent。');
    } finally { $('cookie-input').value = ''; $('save-cookie').textContent = '验证并保存 Cookie ↗'; }
  });
});
$('validate-cookie').addEventListener('click', () => action(async () => {await api('/api/config/validate',{method:'POST',body:'{}'});notify('当前 Cookie 有效。');}));
$('clear-cookie').addEventListener('click', () => action(async () => {await api('/api/config/cookie',{method:'DELETE',body:'{}'});notify('已清除本地账号凭据，扫描结果保留。');}));
$('scan-form').addEventListener('submit', event => {
  event.preventDefault();
  action(async () => {
    await api('/api/scan',{method:'POST',body:JSON.stringify({creator_id:$('creator-input').value.trim(),resume:false})});
    offset = 0;
  });
});
$('resume-scan').addEventListener('click', () => action(async () => {await api('/api/scan',{method:'POST',body:JSON.stringify({creator_id:job.creator_id,resume:true})});}));
$('cancel-scan').addEventListener('click', () => action(async () => {await api('/api/scan/cancel',{method:'POST',body:'{}'});}));
$('previous-page').addEventListener('click', () => {offset = Math.max(0,offset-pageSize);refresh();});
$('next-page').addEventListener('click', () => {offset += pageSize;refresh();});
async function download(format) {
  const response = await request(`/api/scan/export?format=${format}`);
  const blob = await response.blob();
  const link = document.createElement('a'); link.href = URL.createObjectURL(blob); link.download = `up-${job.creator_id}-${job.state === 'complete' ? 'complete' : 'partial'}.${format}`;
  link.click(); setTimeout(() => URL.revokeObjectURL(link.href),1000);
}
$('export-txt').addEventListener('click', () => action(() => download('txt')));
$('export-json').addEventListener('click', () => action(() => download('json')));
$('copy-ids').addEventListener('click', () => action(async () => {
  if (!navigator.clipboard?.writeText) throw new Error('当前浏览器不支持剪贴板写入，请使用 TXT 导出。');
  const text = await (await request('/api/scan/export?format=txt')).text();
  await navigator.clipboard.writeText(text); notify('已复制当前全部视频 ID。');
}));
const token = new URLSearchParams(location.hash.slice(1)).get('token');
if (location.hash) history.replaceState(null,'',location.pathname + location.search);
connect(token).catch(error => {connection('等待连接',false);$('connect-card').hidden = false;notify(error.message,true);buttons();});
