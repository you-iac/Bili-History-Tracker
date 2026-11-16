// detail.js 编排独立详情、真实趋势与稳定字段版本对比；复用Agent既有详情路径。见 docs/client-catalog.md。
import {compactNumber,signedNumber,utcDay,snapshotChanges,followState,publishedAt} from './view.mjs';
import {renderTrend} from './trend.mjs';
import {cardStatus} from './cards.js';
import {snapshotImageURL} from './images.js';
import {initCreatorVideos} from './creator-videos.js';

export function initDetail({api,notify,onAdd,onChanged,onScan,onSyncSummaries,images}) {
  const $=id=>document.getElementById(id);
  const videos=initCreatorVideos({api,notify,onAdd,onChanged,onSyncSummaries,images});
  const node=(tag,text,className)=>{const el=document.createElement(tag);if(text!=null)el.textContent=text;if(className)el.className=className;return el;};
  let route=null,usable=false,working=false,sequence=0,snapshotSequence=0,days='30',shownDays='30',page=1,selected=0,data=null,snapshotBusy=false;
  const date=value=>value?new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'尚无成功观测';
  function url(seq=0){const q=new URLSearchParams({page:String(page),days});if(seq)q.set('seq',String(seq));return `/api/library/${route.kind}/${encodeURIComponent(route.id)}?${q}`;}
  function controls(){const disabled=!usable||working||!data;for(const id of ['detail-days','detail-refresh','detail-follow','detail-unfollow','detail-scan','detail-previous','detail-next'])$(id).disabled=disabled;
    $('detail-refresh').disabled=!usable||working;
    $('detail-previous').disabled=disabled||page<=1;$('detail-next').disabled=disabled||page*(data?.version_limit||10)>=(data?.version_total||0);
    $('detail-follow').disabled=disabled||Boolean(data?.local_follow&&!data.local_follow.pending_submission);
    for(const el of $('detail-versions').querySelectorAll('button'))el.disabled=disabled||snapshotBusy;
  }
  function render(d){
    const creator=route.kind==='creator',item=d.item,local=d.local_follow;shownDays=days;
    $('detail-name').textContent=item.name||route.id;$('detail-identity').textContent=`${creator?'UID ':''}${route.id}${!creator&&item.owner_name?` · ${item.owner_name}`:''}`;
    $('detail-avatar').textContent=creator?(item.name||route.id).slice(0,1):'▷';$('detail-avatar').classList.toggle('detail-cover',!creator);$('detail-avatar').closest('section').classList.toggle('video-hero',!creator);images.show($('detail-avatar'),{kind:route.kind,id:route.id,seq:item.seq});$('detail-record-status').textContent=cardStatus(item);$('detail-record-status').dataset.state=item.status||'unknown';
    $('detail-local-state').textContent=local?`已本地关注 · ${followState(local)}`:'尚未本地关注';
    $('detail-follow').textContent=local?(local.pending_submission?'重试提交':'已本地关注'):'关注并跟踪';$('detail-unfollow').hidden=!local;$('detail-scan').hidden=!creator;
    $('detail-source').textContent=`数据来自 ${d.server_url} · 最近观测 ${date(item.observed_at)}`;
    $('detail-external').href=creator?`https://space.bilibili.com/${route.id}`:`https://www.bilibili.com/video/${route.id}`;
    $('detail-metric-label').textContent=creator?'最新粉丝数':'最新播放量';$('detail-latest').textContent=compactNumber(item.metric);$('detail-latest').title=item.metric?.toLocaleString('zh-CN')||'未知';
    $('detail-total-versions').textContent=compactNumber(d.version_total??item.seq);$('detail-published').textContent=creator?(local?'已关注':'未关注'):publishedAt(item.published_at);
    $('detail-fourth-label').textContent=creator?'个人关注状态':'视频发布时间';
    $('detail-chart-title').textContent=creator?'粉丝数变化':'播放量变化';
    const model=renderTrend($('detail-chart'),d.metrics?.points,creator?'粉丝数变化':'播放量变化');
    $('detail-change').textContent=signedNumber(model.change);$('detail-change').className=model.change>0?'positive':model.change<0?'negative':'';
    $('detail-change-period').textContent=model.points.length>1?`${utcDay(model.points[0].at)} 至 ${utcDay(model.points.at(-1).at)} · UTC`:'至少需要两个有效日采样';
    $('detail-metric-note').textContent=`${model.points.length} 个真实日采样 · UTC日期 · 缺失日期保留缺口${d.metrics?.truncated?' · 已达服务器采样上限':''}`;
    const rows=document.createDocumentFragment();for(const p of model.points){const row=node('tr');row.append(node('td',utcDay(p.at)),node('td',p.value.toLocaleString('zh-CN')));rows.append(row);}if(!model.points.length){const row=node('tr'),cell=node('td','暂无有效采样');cell.colSpan=2;row.append(cell);rows.append(row);}$('detail-metrics').replaceChildren(rows);
    const versions=document.createDocumentFragment();for(const version of d.versions||[]){const button=node('button',null,'version-button');button.type='button';button.dataset.seq=version.seq;button.append(node('strong',`v${version.seq} · ${version.seq===1?'首次记录':'内容更新'}`),node('span',date(version.at)),node('small',version.name||''));button.addEventListener('click',()=>selectSnapshot(version.seq));versions.append(button);}
    if(!d.versions?.length)versions.append(node('p','尚无可查看的完整内容版本。','empty-copy'));$('detail-versions').replaceChildren(versions);
    $('detail-version-page').textContent=`第 ${page} / ${Math.max(1,Math.ceil((d.version_total||0)/(d.version_limit||10)))} 页 · ${d.version_total||0} 个版本`;
    $('detail-error').hidden=!d.cached&&!d.cache_error;if(d.cache_error)$('detail-error').textContent='资料已读取，本地缓存保存失败：'+d.cache_error;if(d.cached)$('detail-error').textContent=`服务器暂不可达，当前为本地缓存（最近核对 ${date(local?.updated_at)}）。${d.read_error||''}`;
    $('detail-body').hidden=false;markSelected();controls();
  }
  function markSelected(){for(const button of $('detail-versions').querySelectorAll('button'))button.setAttribute('aria-pressed',String(Number(button.dataset.seq)===selected));}
  async function selectSnapshot(seq){
    if(!route||!usable||snapshotBusy)return;
    const token=++snapshotSequence,generation=sequence;snapshotBusy=true;controls();$('detail-snapshot-note').textContent=`正在读取 v${seq}…`;
    try{
      const after=await api(url(seq));
      if(token!==snapshotSequence||generation!==sequence)return;
      let before=null,previousError='';
      if(seq>1){try{before=(await api(url(seq-1))).snapshot;}catch(error){previousError=error.message;}}
      if(token!==snapshotSequence||generation!==sequence)return;
      if(seq===data?.item.seq){const cover=snapshotImageURL(route.kind,after.snapshot);if(cover)images.show($('detail-avatar'),{kind:route.kind,id:route.id,seq,url:cover});}
      selected=seq;markSelected();$('detail-snapshot-title').textContent=`v${seq} 的内容变化`;$('detail-snapshot').textContent=JSON.stringify(after.snapshot??{},null,2);
      const changes=snapshotChanges(route.kind,before,after.snapshot),fragment=document.createDocumentFragment();
      if(seq===1){$('detail-snapshot-note').textContent='首次记录作为后续对比基线；计数变化不生成内容版本。';}
      else if(previousError||!before){$('detail-snapshot-note').textContent=`当前版本已读取，上一版本未能读取，暂不展示差异。${previousError}`;}
      else {$('detail-snapshot-note').textContent=changes.length?`与 v${seq-1} 比较，${changes.length} 项内容发生变化。`:'可展示的稳定内容字段没有差异；可展开原始快照查看。';}
      if(seq===1||before)for(const change of changes){const row=node('article',null,'content-change');row.append(node('h3',change.label));if(seq>1){const old=node('div',null,'before-value');old.append(node('span','之前'),node('pre',change.before||'（空）'));row.append(old);}const next=node('div',null,'after-value');next.append(node('span',seq===1?'首次记录':'之后'),node('pre',change.after||'（空）'));row.append(next);fragment.append(row);}
      $('detail-changes').replaceChildren(fragment);
    }catch(error){if(token===snapshotSequence&&generation===sequence){$('detail-snapshot-note').textContent=`快照读取失败：${error.message}`;}}
    finally{if(token===snapshotSequence){snapshotBusy=false;controls();}}
  }
  async function load(){
    if(!route||!usable)return;
    const token=++sequence;snapshotSequence++;snapshotBusy=false;working=true;controls();$('detail-loading').hidden=false;
    try{const d=await api(url());if(token!==sequence)return;data=d;page=d.version_page||page;render(d);if(!selected&&d.item.seq>0&&!d.cached)await selectSnapshot(d.versions?.[0]?.seq||d.item.seq);else if(d.cached&&!selected)$('detail-snapshot-note').textContent='离线缓存保留默认30日指标；历史快照需要连接服务器。';}
    catch(error){if(token===sequence){if(data){days=shownDays;$('detail-days').value=days;page=data.version_page||1;}$('detail-error').textContent=`${error.message}${data?' 已保留上次成功读取的时间范围与资料。':''}`;$('detail-error').hidden=false;}}
    finally{if(token===sequence){working=false;$('detail-loading').hidden=true;controls();}}
  }
  async function act(work){if(working||!usable)return;working=true;controls();try{await work();await onChanged();}catch(error){notify(error.message,true);}finally{working=false;controls();}await load();}
  $('detail-days').addEventListener('change',()=>{days=$('detail-days').value;load();});
  $('detail-refresh').addEventListener('click',()=>load());
  $('detail-follow').addEventListener('click',()=>act(()=>onAdd(route.kind,route.id)));
  $('detail-unfollow').addEventListener('click',()=>act(async()=>{await api(`/api/follows/${route.kind}/${route.id}`,{method:'DELETE',body:'{}'});notify('已取消本地关注，服务器公共采集继续。');}));
  $('detail-scan').addEventListener('click',()=>{if(route?.kind==='creator')videos.start();});
  $('detail-previous').addEventListener('click',()=>{page--;load();});$('detail-next').addEventListener('click',()=>{page++;load();});
  let chartWidth=0;
  new ResizeObserver(()=>{const width=$('detail-chart').clientWidth;if(width&&width!==chartWidth){chartWidth=width;if(route&&data)renderTrend($('detail-chart'),data.metrics?.points,route.kind==='creator'?'粉丝数变化':'播放量变化');}}).observe($('detail-chart'));
  return {
    setConnected(value){usable=value;videos.setConnected(value);controls();},
    tick(){return videos.tick();},
    leave(){sequence++;snapshotSequence++;snapshotBusy=false;working=false;route=null;videos.leave();},
    async show(next){sequence++;snapshotSequence++;snapshotBusy=false;working=false;route=next;videos.show(route.kind==='creator'?route.id:'');days='30';page=1;selected=0;data=null;$('detail-days').value=days;$('detail-body').hidden=true;$('detail-loading').hidden=true;$('detail-error').hidden=usable;if(!usable)$('detail-error').textContent='等待连接本地 Agent，连接后可刷新资料。';$('detail-changes').replaceChildren();$('detail-snapshot').textContent='';$('detail-snapshot-title').textContent='内容变化';$('detail-snapshot-note').textContent='选择版本查看变化。';$('detail-back').href=`#${route.section}${route.scope==='public'?'?scope=public':''}`;controls();await load();}
  };
}
