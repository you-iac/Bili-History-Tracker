// detail-sources.mjs 独立读取作者当前资料与历史；缓存先展示，旧缓存不覆盖新结果。
// 不加载版本或视频摘要。见 docs/client-detail-data-sources.md。
export async function readCreatorSources({api,id,days='30',refresh=false,historyOnly=false,signal,onCurrent,onHistory,onError}) {
  const target=encodeURIComponent(id),options=signal?{signal}:undefined;
  let fresh=false;
  const jobs=[];
  if(!historyOnly){
    jobs.push(api(`/api/current/creator/${target}/cache`,options).then(value=>{if(!fresh)onCurrent(value);}).catch(()=>{}));
    jobs.push(api(`/api/current/creator/${target}${refresh?'?refresh=1':''}`,options).then(value=>{fresh=true;onCurrent(value);}).catch(error=>onError('current',error)));
  }
  jobs.push(api(`/api/history/creator/${target}?days=${encodeURIComponent(days)}`,options).then(onHistory).catch(error=>onError('history',error)));
  await Promise.all(jobs);
}
