// view.mjs 集中处理状态和日期显示，真实数量缺失时保留未知，不推算成完整结果。
export function stateLabel(state) {
  return ({running:'扫描中',cancelling:'正在停止',complete:'扫描完成',partial:'部分结果',cancelled:'已停止',interrupted:'待继续'})[state] || '等待开始';
}
export function progress(count, total, state) {
  if (state === 'complete') return 100;
  if (!Number.isFinite(total) || total <= 0) return 0;
  return Math.max(0, Math.min(99, Math.floor(count / total * 100)));
}
export function publishedAt(timestamp) {
  if (!Number.isFinite(timestamp) || timestamp <= 0) return '—';
  // 平台扫描返回秒，既有服务器目录返回毫秒；兼容已保存的两类资料。
  return new Intl.DateTimeFormat('zh-CN', {timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date(timestamp < 1e12 ? timestamp * 1000 : timestamp));
}
export function rangeLabel(offset, length, total) {
  return length > 0 ? `${offset + 1}–${offset + length} / 共 ${total.toLocaleString('zh-CN')} 条` : `共 ${total.toLocaleString('zh-CN')} 条视频 ID`;
}
// 路由白名单避免启动密钥或任意片段被当成页面名。
export function routeForHash(hash) {
  const [path,query=''] = String(hash||'').replace(/^#/,'').split('?');
  const [section,id,...rest] = path.split('/');
  const scope = new URLSearchParams(query).get('scope') === 'public' ? 'public' : '';
  const kind = section === 'creators' ? 'creator' : section === 'videos' ? 'video' : '';
  const validID = kind === 'creator' ? /^[1-9]\d{0,19}$/.test(id||'') : /^BV[1-9A-HJ-NP-Za-km-z]{10}$/.test(id||'');
  if(kind&&id&&validID&&!rest.length) return {view:'detail',kind,id,scope,section,hash:`#${section}/${id}${scope?'?scope=public':''}`};
  if(!id&&!rest.length&&['overview','creators','videos','discovery','settings'].includes(section)) return {view:section,kind,scope,section,hash:`#${section}${kind&&scope?'?scope=public':''}`};
  return {view:'overview',kind:'',scope:'',section:'overview',hash:'#overview'};
}
export function viewForHash(hash) {return routeForHash(hash).view;}
export function detailHref(kind,id,scope='') {return routeForHash(`#${kind==='creator'?'creators':'videos'}/${id}${scope==='public'?'?scope=public':''}`).hash;}
export function compactNumber(value) {
  if(!Number.isFinite(value)) return '—';
  if(Math.abs(value)>=1e8) return `${(value/1e8).toLocaleString('zh-CN',{maximumFractionDigits:1})}亿`;
  if(Math.abs(value)>=1e4) return `${(value/1e4).toLocaleString('zh-CN',{maximumFractionDigits:1})}万`;
  return value.toLocaleString('zh-CN');
}
export function signedNumber(value) {return Number.isFinite(value)?`${value>0?'+':''}${compactNumber(value)}`:'—';}
export function utcDay(value) {return Number.isFinite(value)&&value>0?new Date(value).toISOString().slice(0,10):'—';}

// 只比较稳定内容字段，播放/粉丝等计数不会被列为内容修改。
export function snapshotChanges(kind,before,after) {
  const creator=kind==='creator',a=creator?(before?.card||before||{}):(before||{}),b=creator?(after?.card||after||{}):(after||{});
  const fields=creator?[['name','名称'],['sign','签名'],['face','头像']]:[['title','标题'],['desc','简介'],['pic','封面'],['copyright','版权类型'],['duration','时长（秒）'],['pages','分P信息'],['staff','联合创作']];
  const value=(source,key)=>source[key] == null?'':typeof source[key]==='object'?JSON.stringify(source[key],null,2):String(source[key]);
  return fields.flatMap(([key,label])=>{const old=value(a,key),next=value(b,key);return old===next?[]:[{key,label,before:old,after:next}];});
}

export function followState(follow) {
	if (follow.needs_attention) return '需要手动核对';
	if (follow.last_code === 'TARGET_PAUSED' || follow.remote?.tracking_state === 'paused' || follow.remote?.status === 'paused') return '服务器已暂停';
	if (follow.last_error && follow.remote?.tracking) return '最近确认已跟踪 · 本次核对未完成';
  if (follow.pending_submission) return follow.last_error ? '待提交 · 后台将重试' : '正在申请服务器跟踪';
  if (!follow.remote) return '服务器状态未查询';
  if (follow.remote.tracking) return '服务器跟踪中';
  if (follow.remote.status === 'paused') return '服务器已暂停';
  return follow.remote.recorded ? '服务器已记录，未跟踪' : '服务器尚未记录';
}

// 缓存是数据来源；错误业务码才决定缺资料、限流或连接失败。
export function detailReadMessage(detail) {
  if (detail.cache_error) return '资料已读取，本地缓存保存失败：'+detail.cache_error;
  if (!detail.read_error) return detail.cached?'当前显示本地缓存。':'';
  const labels={DETAIL_NOT_RECORDED:detail.target?.tracking?'服务器已跟踪，暂无成功观测资料。':'服务器暂无成功观测资料。',RATE_LIMITED:'服务器请求较多，请稍后重试。',TARGET_BUSY:'目标正在处理中，请稍后刷新。',TARGET_PAUSED:'服务器已暂停此目标。',CONNECTION_FAILED:'无法连接服务器。'};
  return (labels[detail.read_code]||detail.read_error)+(detail.cached?' 当前显示本地缓存。':'');
}
