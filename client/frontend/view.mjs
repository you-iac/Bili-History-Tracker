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
  return new Intl.DateTimeFormat('zh-CN', {timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date(timestamp * 1000));
}
export function rangeLabel(offset, length, total) {
  return length > 0 ? `${offset + 1}–${offset + length} / 共 ${total.toLocaleString('zh-CN')} 条` : `共 ${total.toLocaleString('zh-CN')} 条视频 ID`;
}
// 路由白名单避免启动密钥或任意片段被当成页面名。
export function viewForHash(hash) {
  const view = String(hash || '').replace(/^#/,'');
  return ['overview','creators','videos','discovery','settings'].includes(view) ? view : 'overview';
}

export function followState(follow) {
  if (follow.pending_submission) return follow.last_error ? '待提交 · 后台将重试' : '正在申请服务器跟踪';
  if (!follow.remote) return '服务器状态未查询';
  if (follow.remote.tracking) return '服务器跟踪中';
  if (follow.remote.status === 'paused') return '服务器已暂停';
  return follow.remote.recorded ? '服务器已记录，未跟踪' : '服务器尚未记录';
}
