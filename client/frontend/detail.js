// detail.js 将作者平台资料与服务器历史独立展示，版本/对比按需；视频详情保留兼容路径。
// 来源、查询预算与缓存约束见 docs/client-detail-data-sources.md、docs/client-catalog.md。
import {compactNumber,signedNumber,utcDay,snapshotChanges,followState,publishedAt,detailReadMessage} from './view.mjs';
import {renderTrend} from './trend.mjs';
import {cardStatus} from './cards.js';
import {snapshotImageURL} from './images.js';
import {initCreatorVideos} from './creator-videos.js';
import {readCreatorSources} from './detail-sources.mjs';

export function initDetail({api,notify,onAdd,onChanged,onScan,onSyncSummaries,images}) {
  const $=id=>document.getElementById(id);
  const videos=initCreatorVideos({api,notify,onAdd,onChanged,onSyncSummaries,images});
  const node=(tag,text,className)=>{const el=document.createElement(tag);if(text!=null)el.textContent=text;if(className)el.className=className;return el;};
  let route=null,usable=false,working=false,sequence=0,snapshotSequence=0,days='30',shownDays='30',page=1,selected=0,data=null,snapshotBusy=false;
  let currentView=null,historyView=null,creatorErrors={},creatorVersions=[],versionCursors=[0],nextBefore=0,versionsLoaded=false,versionBusy=false,versionSequence=0,readController=null,selectedSnapshot=null,versionController=null,snapshotController=null;
  const date=value=>value?new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'尚无成功观测';
  function url(seq=0){const q=new URLSearchParams({page:String(page),days});if(seq)q.set('seq',String(seq));return `/api/library/${route.kind}/${encodeURIComponent(route.id)}?${q}`;}
  function controls(){const disabled=!usable||working||!data;for(const id of ['detail-days','detail-refresh','detail-follow','detail-unfollow','detail-scan','detail-previous','detail-next'])$(id).disabled=disabled;
    $('detail-refresh').disabled=!usable||working;
    $('detail-previous').disabled=disabled||page<=1||versionBusy;$('detail-next').disabled=disabled||versionBusy||(route?.kind==='creator'?!nextBefore:page*(data?.version_limit||10)>=(data?.version_total||0));
    $('detail-follow').disabled=disabled||Boolean(data?.local_follow&&!data.local_follow.pending_submission&&!data.local_follow.needs_attention);
    for(const el of $('detail-versions').querySelectorAll('button'))el.disabled=disabled||snapshotBusy;
    $('detail-compare').hidden=route?.kind!=='creator';$('detail-compare').disabled=disabled||snapshotBusy||selected<=1||!selectedSnapshot;
  }
  function render(d){
    const creator=route.kind==='creator',item=d.item,local=d.local_follow;shownDays=days;
    $('detail-name').textContent=item.name||route.id;$('detail-identity').textContent=`${creator?'UID ':''}${route.id}${!creator&&item.owner_name?` · ${item.owner_name}`:''}`;
    $('detail-avatar').textContent=creator?(item.name||route.id).slice(0,1):'▷';$('detail-avatar').classList.toggle('detail-cover',!creator);$('detail-avatar').closest('section').classList.toggle('video-hero',!creator);images.show($('detail-avatar'),{kind:route.kind,id:route.id,seq:item.seq,url:item.cover||'',resolve:!creator});$('detail-record-status').textContent=creator?(historyView?cardStatus({status:historyView.target.status,tracking:historyView.target.tracking}):'服务器状态未知'):cardStatus(item);$('detail-record-status').dataset.state=(creator?historyView?.target.status:item.status)||'unknown';
    $('detail-local-state').textContent=local?`已本地关注 · ${followState(local)}`:'尚未本地关注';
    $('detail-follow').textContent=local?(local.needs_attention?'重新核对':local.pending_submission?'重试提交':'已本地关注'):'关注并跟踪';$('detail-unfollow').hidden=!local;$('detail-scan').hidden=!creator;
    $('detail-source').textContent=creator?(currentView?.available?`${currentView.source==='platform'?'平台当前资料':`服务器最后记录 v${item.seq} · ${currentView.server_url}`} · 观测 ${date(item.observed_at)}${currentView.cached?' · 本地缓存':''}`:'当前资料暂不可获取'):`数据来自 ${d.server_url} · 最近观测 ${date(item.observed_at)}`;
    $('detail-description').textContent=creator?(currentView?.description||''):'';
    $('detail-record-time').textContent=creator&&historyView?`服务器最近资料观测 ${date(historyView.record.observed_at)}${historyView.cached?' · 缓存':''}`:'';
    $('detail-external').href=creator?`https://space.bilibili.com/${route.id}`:`https://www.bilibili.com/video/${route.id}`;
    $('detail-metric-label').textContent=creator?'最新粉丝数':'最新播放量';$('detail-latest').textContent=compactNumber(item.metric);$('detail-latest').title=item.metric?.toLocaleString('zh-CN')||'未知';
    $('detail-version-label').textContent=creator?'服务器当前版本':'内容版本';$('detail-total-versions').textContent=creator?(historyView?(d.record?.seq?`v${d.record.seq}`:'尚未记录'):'未知'):compactNumber(d.version_total??item.seq);$('detail-published').textContent=creator?(local?'已关注':'未关注'):publishedAt(item.published_at);
    $('detail-fourth-label').textContent=creator?'个人关注状态':'视频发布时间';
    $('detail-chart-title').textContent=creator?'粉丝数变化':'播放量变化';
    const model=renderTrend($('detail-chart'),d.metrics?.points,creator?'粉丝数变化':'播放量变化');
    $('detail-change').textContent=signedNumber(model.change);$('detail-change').className=model.change>0?'positive':model.change<0?'negative':'';
    $('detail-change-period').textContent=model.points.length>1?`${utcDay(model.points[0].at)} 至 ${utcDay(model.points.at(-1).at)} · UTC`:'至少需要两个有效日采样';
    $('detail-metric-note').textContent=`${model.points.length} 个真实日采样 · UTC日期 · 缺失日期保留缺口${d.metrics?.truncated?' · 已达服务器采样上限':''}`;
    const rows=document.createDocumentFragment();for(const p of model.points){const row=node('tr');row.append(node('td',utcDay(p.at)),node('td',p.value.toLocaleString('zh-CN')));rows.append(row);}if(!model.points.length){const row=node('tr'),cell=node('td','暂无有效采样');cell.colSpan=2;row.append(cell);rows.append(row);}$('detail-metrics').replaceChildren(rows);
    const versions=document.createDocumentFragment();for(const version of d.versions||[]){const button=node('button',null,'version-button');button.type='button';button.dataset.seq=version.seq;button.append(node('strong',`v${version.seq} · ${version.seq===1?'首次记录':'内容更新'}`),node('span',date(version.at)),node('small',version.name||''));button.addEventListener('click',()=>selectSnapshot(version.seq));versions.append(button);}
    if(!d.versions?.length)versions.append(node('p','尚无可查看的完整内容版本。','empty-copy'));$('detail-versions').replaceChildren(versions);
    $('detail-version-page').textContent=creator?(versionsLoaded?`第 ${page} 页${nextBefore?' · 还有更早版本':' · 已到最早版本'}`:'展开后读取版本列表'):`第 ${page} / ${Math.max(1,Math.ceil((d.version_total||0)/(d.version_limit||10)))} 页 · ${d.version_total||0} 个版本`;
    const readMessage=creator?[creatorErrors.current,creatorErrors.history,currentView?.read_error,currentView?.cache_error,currentView?.fallback_reason&&`平台资料读取失败：${currentView.fallback_reason}`,historyView?.read_error,historyView?.cache_error].filter(Boolean).join('；'):detailReadMessage(d);$('detail-error').hidden=!readMessage;$('detail-error').textContent=readMessage;
    $('detail-body').hidden=false;markSelected();controls();
  }
  function markSelected(){for(const button of $('detail-versions').querySelectorAll('button'))button.setAttribute('aria-pressed',String(Number(button.dataset.seq)===selected));}
  async function selectSnapshot(seq,compare=false){
    if(!route||!usable||snapshotBusy)return;
    const token=++snapshotSequence,generation=sequence;snapshotBusy=true;controls();$('detail-snapshot-note').textContent=`正在读取 v${seq}…`;
    try{
      const creator=route.kind==='creator',snapshotURL=n=>`/api/history/creator/${encodeURIComponent(route.id)}/snapshot?seq=${n}`;
      if(creator){snapshotController?.abort();snapshotController=new AbortController();}
      const options=creator?{signal:snapshotController.signal}:undefined;
      const raw=creator&&compare&&selected===seq&&selectedSnapshot?{snapshot:selectedSnapshot}:await api(creator?snapshotURL(seq):url(seq),options);
      const after=creator?{snapshot:raw.snapshot}:raw;
      if(token!==snapshotSequence||generation!==sequence)return;
      let before=null,previousError='';
      if(seq>1&&(!creator||compare)){try{before=(await api(creator?snapshotURL(seq-1):url(seq-1),options)).snapshot;}catch(error){previousError=error.message;}}
      if(token!==snapshotSequence||generation!==sequence)return;
      if(!creator&&seq===data?.item.seq){const cover=snapshotImageURL(route.kind,after.snapshot);if(cover)images.show($('detail-avatar'),{kind:route.kind,id:route.id,seq,url:cover});}
      selected=seq;selectedSnapshot=after.snapshot;markSelected();$('detail-snapshot-title').textContent=`v${seq} 的内容变化`;$('detail-snapshot').textContent=JSON.stringify(after.snapshot??{},null,2);
      const changes=snapshotChanges(route.kind,before,after.snapshot),fragment=document.createDocumentFragment();
      if(seq===1){$('detail-snapshot-note').textContent='首次记录作为后续对比基线；计数变化不生成内容版本。';}
      else if(creator&&!compare){$('detail-snapshot-note').textContent=`已读取 v${seq}；点击“与上一版本比较”后再读取 v${seq-1}。`;}
      else if(previousError||!before){$('detail-snapshot-note').textContent=`当前版本已读取，上一版本未能读取，暂不展示差异。${previousError}`;}
      else {$('detail-snapshot-note').textContent=changes.length?`与 v${seq-1} 比较，${changes.length} 项内容发生变化。`:'可展示的稳定内容字段没有差异；可展开原始快照查看。';}
      if(seq===1||before)for(const change of changes){const row=node('article',null,'content-change');row.append(node('h3',change.label));if(seq>1){const old=node('div',null,'before-value');old.append(node('span','之前'),node('pre',change.before||'（空）'));row.append(old);}const next=node('div',null,'after-value');next.append(node('span',seq===1?'首次记录':'之后'),node('pre',change.after||'（空）'));row.append(next);fragment.append(row);}
      $('detail-changes').replaceChildren(fragment);
    }catch(error){if(token===snapshotSequence&&generation===sequence){$('detail-snapshot-note').textContent=`快照读取失败：${error.message}`;}}
    finally{if(token===snapshotSequence){snapshotBusy=false;controls();}}
  }
  function renderCreator(){
    if(!route||route.kind!=='creator')return;
    data={item:currentView?.available?currentView.item:{kind:'creator',id:route.id},local_follow:currentView?.local_follow,record:historyView?.record,metrics:historyView?.metrics,versions:creatorVersions};render(data);
  }
  async function loadCreator(refresh=false,historyOnly=false){
    const token=++sequence;readController?.abort();snapshotController?.abort();snapshotSequence++;snapshotBusy=false;readController=new AbortController();working=true;controls();$('detail-loading').hidden=false;
    await readCreatorSources({api,id:route.id,days,refresh,historyOnly,signal:readController.signal,
      onCurrent:value=>{if(token!==sequence)return;currentView=value;delete creatorErrors.current;renderCreator();},
      onHistory:value=>{if(token!==sequence)return;historyView=value;delete creatorErrors.history;renderCreator();},
      onError:(part,error)=>{if(token!==sequence)return;creatorErrors[part]=`${part==='current'?'当前资料':'服务器历史'}：${error.message}`;if(part==='history'&&historyView){days=shownDays;$('detail-days').value=days;}renderCreator();}
    });
    if(token===sequence){working=false;$('detail-loading').hidden=true;controls();}
  }
  async function loadCreatorVersions(requested=versionCursors){
    if(!route||route.kind!=='creator'||versionBusy)return;
    const owner=route.id,token=++versionSequence;versionController?.abort();versionController=new AbortController();versionBusy=true;controls();
    try{const before=requested.at(-1),value=await api(`/api/history/creator/${encodeURIComponent(owner)}/versions${before?`?before=${before}`:''}`,{signal:versionController.signal});if(token!==versionSequence||route?.id!==owner)return;versionCursors=[...requested];page=versionCursors.length;creatorVersions=value.items;nextBefore=value.next_before;versionsLoaded=true;renderCreator();}
    catch(error){if(token===versionSequence){$('detail-snapshot-note').textContent=`版本读取失败：${error.message}`;}}
    finally{if(token===versionSequence){versionBusy=false;controls();}}
  }
  async function load(refresh=false,historyOnly=false){
    if(!route||!usable)return;
    if(route.kind==='creator')return loadCreator(refresh,historyOnly);
    const token=++sequence;snapshotSequence++;snapshotBusy=false;working=true;controls();$('detail-loading').hidden=false;
    try{const d=await api(url());if(token!==sequence)return;data=d;page=d.version_page||page;render(d);if(!selected&&d.item.seq>0&&!d.cached)await selectSnapshot(d.versions?.[0]?.seq||d.item.seq);else if(d.cached&&!selected)$('detail-snapshot-note').textContent='离线缓存保留默认30日指标；历史快照需要连接服务器。';}
    catch(error){if(token===sequence){if(data){days=shownDays;$('detail-days').value=days;page=data.version_page||1;}$('detail-error').textContent=`${error.message}${data?' 已保留上次成功读取的时间范围与资料。':''}`;$('detail-error').hidden=false;}}
    finally{if(token===sequence){working=false;$('detail-loading').hidden=true;controls();}}
  }
  async function act(work){if(working||!usable)return;working=true;controls();try{await work();await onChanged();}catch(error){notify(error.message,true);}finally{working=false;controls();}await load();}
  $('detail-days').addEventListener('change',()=>{days=$('detail-days').value;load(false,true);});
  $('detail-refresh').addEventListener('click',()=>load(true));
  $('detail-follow').addEventListener('click',()=>act(()=>onAdd(route.kind,route.id)));
  $('detail-unfollow').addEventListener('click',()=>act(async()=>{await api(`/api/follows/${route.kind}/${route.id}`,{method:'DELETE',body:'{}'});notify('已取消本地关注，服务器公共采集继续。');}));
  $('detail-scan').addEventListener('click',()=>{if(route?.kind==='creator')videos.start();});
  $('detail-previous').addEventListener('click',()=>{if(route?.kind==='creator'){if(versionBusy||page<=1)return;loadCreatorVersions(versionCursors.slice(0,-1));}else{page--;load();}});$('detail-next').addEventListener('click',()=>{if(route?.kind==='creator'){if(versionBusy||!nextBefore)return;loadCreatorVersions([...versionCursors,nextBefore]);}else{page++;load();}});
  $('detail-history').addEventListener('toggle',()=>{if($('detail-history').open&&route?.kind==='creator')loadCreatorVersions([0]);});
  $('detail-compare').addEventListener('click',()=>selectSnapshot(selected,true));
  let chartWidth=0;
  new ResizeObserver(()=>{const width=$('detail-chart').clientWidth;if(width&&width!==chartWidth){chartWidth=width;if(route&&data)renderTrend($('detail-chart'),data.metrics?.points,route.kind==='creator'?'粉丝数变化':'播放量变化');}}).observe($('detail-chart'));
  return {
    setConnected(value){usable=value;videos.setConnected(value);controls();},
    tick(){return videos.tick();},
    leave(){sequence++;snapshotSequence++;versionSequence++;readController?.abort();versionController?.abort();snapshotController?.abort();versionBusy=false;snapshotBusy=false;working=false;route=null;videos.leave();},
    async show(next){sequence++;snapshotSequence++;versionSequence++;readController?.abort();versionController?.abort();snapshotController?.abort();currentView=null;historyView=null;selectedSnapshot=null;creatorErrors={};creatorVersions=[];versionCursors=[0];nextBefore=0;versionsLoaded=false;versionBusy=false;snapshotBusy=false;working=false;route=next;videos.show(route.kind==='creator'?route.id:'');days='30';page=1;selected=0;data=null;$('detail-history').open=route.kind!=='creator';$('detail-days').value=days;$('detail-body').hidden=true;$('detail-loading').hidden=true;$('detail-error').hidden=usable;if(!usable)$('detail-error').textContent='等待连接本地 Agent，连接后可刷新资料。';$('detail-changes').replaceChildren();$('detail-snapshot').textContent='';$('detail-snapshot-title').textContent='内容变化';$('detail-snapshot-note').textContent='选择版本查看变化。';$('detail-back').href=`#${route.section}${route.scope==='public'?'?scope=public':''}`;controls();await load();}
  };
}
