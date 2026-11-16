// creator-videos.js 在 UP 详情内查看关注、扫描投稿和逐条关注；请求代际隔离切换的 UP。
// 所有排序在 Agent 对完整已获取集合应用，未完成扫描持续显示部分结果。见 docs/client-catalog.md。
import {renderCards} from './cards.js';
import {stateLabel} from './view.mjs';

export function initCreatorVideos({api,notify,onAdd,onChanged,onSyncSummaries,images}) {
  const $=id=>document.getElementById(id);
  let uid='',connected=false,working=false,loading=false,sequence=0,lastRead=0,stamp='',scope='followed',sort='published',q='',page=1;
  let result={items:[],page:1,total:0,limit:20},job=null,configured=false,elsewhere=false;
  const running=()=>job?.state==='running'||job?.state==='cancelling';
  function controls(){
    const disabled=!connected||working;
    for(const el of $('creator-videos-panel').querySelectorAll('button,input,select'))el.disabled=disabled;
    $('creator-videos-previous').disabled=disabled||loading||result.page<=1;
    $('creator-videos-next').disabled=disabled||loading||result.page*result.limit>=result.total;
    $('creator-scan-start').disabled=disabled||!configured||running()||elsewhere;
    $('creator-scan-resume').hidden=!job?.can_resume;
    $('creator-scan-resume').disabled=disabled||!configured||elsewhere;
    $('creator-scan-cancel').hidden=!running();
    $('creator-scan-cancel').disabled=disabled||job?.state==='cancelling';
    $('creator-scan-settings').hidden=configured;
  }
  async function act(work){
    if(!connected||working)return;
    const owner=uid;working=true;controls();
    try{await work();await onChanged();}catch(error){notify(error.message,true);}
    finally{working=false;controls();}
    if(uid===owner)await load(true);
  }
  function button(text,action,className='button secondary'){
    const el=document.createElement('button');el.type='button';el.textContent=text;el.className=className;el.disabled=!connected||working;
    el.addEventListener('click',()=>act(action));return el;
  }
  function render(data,force){
    result=data.catalog;page=result.page;job=data.job;configured=data.configured;elsewhere=Boolean(data.busy_elsewhere);
    $('creator-videos-followed').setAttribute('aria-pressed',String(scope==='followed'));
    $('creator-videos-discovered').setAttribute('aria-pressed',String(scope==='discovered'));
    $('creator-videos-count').textContent=`${scope==='followed'?'已关注':'未关注'} ${result.total} 个视频`;
    $('creator-videos-page').textContent=`第 ${page} / ${Math.max(1,Math.ceil(result.total/result.limit))} 页 · 共 ${result.total} 项`;
    const note=job?`${stateLabel(job.state)} · 已获取 ${job.count} / ${job.total??'未知'} 个可见投稿 · ${job.message}`:(elsewhere?'另一个 UP 的扫描正在进行，可在投稿发现中查看。':'获取这位 UP 的可见投稿，选择感兴趣的视频关注。');
    $('creator-scan-note').textContent=note+(scope==='discovered'?' · 此处只显示尚未本地关注的结果。':'');
    if(sort==='hot')$('creator-scan-note').textContent+=' 最近热门使用近7日观测增量；新发现投稿暂无历史采样。';
    const nextStamp=JSON.stringify([scope,result.items]);
    if(force||stamp!==nextStamp){
      stamp=nextStamp;
      renderCards({container:$('creator-videos-rows'),data:result,kind:'video',publicMode:scope==='discovered',button,images,
        onAdd:(kind,id)=>onAdd(kind,id),
        onRefresh:f=>api(`/api/library/video/${f.id}?days=30&page=1`),
        onRetry:f=>api(`/api/follows/video/${f.id}/submit`,{method:'POST',body:'{}'}),
        onRemove:f=>api(`/api/follows/video/${f.id}`,{method:'DELETE',body:'{}'})});
    }
    controls();
  }
  async function load(force=false){
    if(!uid||!connected)return;
    const token=++sequence,owner=uid;loading=true;controls();$('creator-videos-loading').hidden=false;
    try{
      const data=await api(`/api/creator-videos/${owner}?`+new URLSearchParams({scope,sort,q,page:String(page)}));
      if(token!==sequence||owner!==uid)return;
      render(data,force);$('creator-videos-error').hidden=true;lastRead=Date.now();
    }catch(error){if(token===sequence){$('creator-videos-error').textContent=error.message+' 已保留上次读取的结果。';$('creator-videos-error').hidden=false;}}
    finally{if(token===sequence){loading=false;$('creator-videos-loading').hidden=true;controls();}}
  }
  async function start(resume=false){
    const owner=uid;
    await act(async()=>{
      await api('/api/scan',{method:'POST',body:JSON.stringify({creator_id:owner,resume})});
      if(uid===owner){scope='discovered';page=1;q='';$('creator-videos-query').value='';}
    });
  }
  for(const value of ['followed','discovered'])$('creator-videos-'+value).addEventListener('click',()=>{scope=value;page=1;load(true);});
  $('creator-videos-sort').addEventListener('change',()=>{sort=$('creator-videos-sort').value;page=1;load(true);});
  $('creator-videos-search').addEventListener('submit',event=>{event.preventDefault();q=$('creator-videos-query').value.trim();page=1;load(true);});
  for(const [name,delta] of [['previous',-1],['next',1]])$('creator-videos-'+name).addEventListener('click',()=>{page+=delta;load(true);});
  $('creator-scan-start').addEventListener('click',()=>start());$('creator-scan-resume').addEventListener('click',()=>start(true));
  $('creator-scan-cancel').addEventListener('click',()=>act(()=>api('/api/scan/cancel',{method:'POST',body:'{}'})));
  function density(value){$('creator-videos-size').value=value;$('creator-videos-rows').dataset.size=value;}
  let size='standard';try{const saved=localStorage.getItem('bht.card-size');if(['compact','standard','large'].includes(saved))size=saved;}catch{}density(size);
  $('creator-videos-size').addEventListener('change',()=>{const value=$('creator-videos-size').value;density(value);try{localStorage.setItem('bht.card-size',value);}catch{}window.dispatchEvent(new CustomEvent('bht-density',{detail:value}));});
  window.addEventListener('bht-density',event=>density(event.detail));
  return {
    setConnected(value){connected=value;controls();},
    show(id){sequence++;uid=id||'';scope='followed';sort='published';q='';page=1;stamp='';job=null;result={items:[],page:1,total:0,limit:20};$('creator-videos-panel').hidden=!uid;$('creator-videos-rows').replaceChildren();$('creator-videos-query').value='';$('creator-videos-sort').value=sort;$('creator-videos-error').hidden=true;controls();if(uid){const owner=uid;load(true);onSyncSummaries?.('video').then(()=>{if(uid===owner)load(true);}).catch(()=>{});}},
    leave(){sequence++;uid='';loading=false;$('creator-videos-loading').hidden=true;},
    start,
    tick(){if(uid&&connected&&!working&&!loading&&Date.now()-lastRead>2000&&!$('creator-videos-rows').querySelector('details[open]'))return load();}
  };
}
