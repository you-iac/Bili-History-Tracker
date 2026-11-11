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
