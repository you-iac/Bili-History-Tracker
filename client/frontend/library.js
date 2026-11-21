// library.js 编排公开总览、本地库/公开搜索、自动关注与设置；业务请求经本地Agent，图片直连白名单CDN。
import {detailHref,routeForHash} from './view.mjs';
import {renderCards} from './cards.js';
import {initDetail} from './detail.js';
import {initImages} from './images.js';

export function initLibrary({api,notify,onScan,navigate}) {
  const $ = id => document.getElementById(id);
  const images=initImages({api});
  const views = {creator:{page:1,q:'',scope:'local',pending:false,sort:'published'},video:{page:1,q:'',scope:'local',pending:false,sort:'published'}};
  const pageLimits={compact:30,standard:20,large:12};
  let usable=false,agentOnline=false,ready=false,busy=false,server='',kind='creator',current='overview',result={items:[],page:1,total:0,limit:20};
  let lastLocalLoad=0;
  const summaryJobs=new Map(),summaryReads=new Map(),summaryNotes=new Map();
  let summaryEpoch=0;
  let loadSequence=0,overviewSequence=0,dashboard=null,loadedOverview=false,lastRefresh=0;
  let dialogKind='creator',currentRoute=routeForHash(location.hash),overviewStamp='';
  const overviewPages={creator:{page:1,limit:18,total:0,items:[],busy:false,sequence:0},video:{page:1,limit:18,total:0,items:[],busy:false,sequence:0}};
  const number = value => value == null ? '未知' : Number(value).toLocaleString('zh-CN');
  const date = value => value ? new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}) : '尚无观测';
  const path = f => `/api/follows/${f.kind}/${encodeURIComponent(f.id)}`;
  const post = url => api(url,{method:'POST',body:'{}'});
  function node(tag,text,className) {const el=document.createElement(tag);if(text!=null)el.textContent=text;if(className)el.className=className;return el;}
  function button(text,action,className='button secondary') {const el=node('button',text,className);el.type='button';el.disabled=!usable||busy;el.addEventListener('click',()=>run(action));return el;}
  function serverState(text,error=false) {$('server-status').textContent=text;$('server-status').className=`connection-status${error?' offline':''}`;}
  function controls() {
    for(const id of ['library-panel','server-form','directory-form','follow-form','overview-panel'])for(const el of $(id).querySelectorAll('input,button,select'))el.disabled=!usable||busy;
    $('library-previous').disabled=!usable||busy||result.page<=1;
    $('library-next').disabled=!usable||busy||result.page*result.limit>=result.total;
    $('test-server').disabled=!usable||busy||!server;
    $('refresh-library').disabled=!usable||busy||!server||(views[kind].scope==='local'&&!result.items.length);
    $('retry-pending').disabled=!usable||busy||!server||!dashboard?.pending;
    $('overview-refresh').disabled=!usable||busy||!server;
    for(const [k,name] of [['creator','creators'],['video','videos']]){const p=overviewPages[k];$('overview-'+name+'-previous').disabled=!usable||busy||p.busy||p.page<=1;$('overview-'+name+'-next').disabled=!usable||busy||p.busy||!server||p.page*p.limit>=p.total;}
  }
  function counts(value) {
    const oldPending=dashboard?.pending;dashboard=value;server=value.server_url;images.setSource(server);
    $('creator-library-count').textContent=value.creators;$('video-library-count').textContent=value.videos;
    $('local-creators').textContent=value.creators;$('local-videos').textContent=value.videos;$('local-pending').textContent=value.pending;
    controls();return oldPending!==undefined&&oldPending!==value.pending;
  }
  async function localDashboard() {const data=await api('/api/overview');counts(data);return data;}
  function compact(items,container,empty) {
    const rows=document.createDocumentFragment();
    for(const item of items||[]) {const row=node('div',null,'compact-item');const info=node('div',null,'compact-info');info.append(node('strong',item.name||item.id),node('span',`${item.kind==='creator'?'粉丝':'播放'} ${number(item.metric)} · ${item.id}`));const avatar=node('span',(item.name||item.id).slice(0,1),'avatar');images.show(avatar,{kind:item.kind,id:item.id,seq:item.metadata_source?0:item.seq,url:item.cover||''});row.append(avatar,info);const link=node('a','查看 →','text-button');link.href=detailHref(item.kind,item.id,'public');row.append(link);rows.append(row);}
    if(!items?.length)rows.append(node('p',empty,'empty-copy'));$(container).replaceChildren(rows);
  }
  function renderOverview(value) {
    const data=value.public;
    for(const [field,id] of [['creators','public-creators'],['videos','public-videos']])$(id).textContent=data?number(data.summary[field]):'—';
    $('overview-time').textContent=data?`上次读取 ${date(data.fetched_at)}`:'尚未读取';
    if(data&&overviewStamp!==data.fetched_at){overviewStamp=data.fetched_at;for(const [k,name,limit] of [['creator','creators','creator_limit'],['video','videos','video_limit']]){Object.assign(overviewPages[k],{page:1,limit:data[limit]||18,total:data.summary[name],items:data[name]||[],busy:false});overviewPages[k].sequence++;$('overview-'+name+'-error').hidden=true;}}
    if(!data){overviewStamp='';for(const p of Object.values(overviewPages)){p.sequence++;Object.assign(p,{items:[],total:0,page:1,busy:false});}}
    renderOverviewPage('creator');renderOverviewPage('video');
    loadedOverview=true;
  }
  function renderOverviewPage(k){const p=overviewPages[k],name=k==='creator'?'creators':'videos';compact(p.items,'overview-'+name,server?'暂无已读取的公开记录。':'在设置中配置服务器后查看公开资料。');$('overview-'+name+'-page').textContent=overviewStamp?('第 '+p.page+' / '+Math.max(1,Math.ceil(p.total/p.limit))+' 页 · 共 '+p.total+' 条'):'尚未读取';controls();}
  async function overviewPage(k,page){const p=overviewPages[k],name=k==='creator'?'creators':'videos',sequence=++p.sequence,base=server;p.busy=true;controls();try{const data=await api('/api/remote/catalog?'+new URLSearchParams({kind:k,page:String(page)}));if(sequence!==p.sequence||base!==server)return;Object.assign(p,data);$('overview-'+name+'-error').hidden=true;renderOverviewPage(k);}catch(error){if(sequence===p.sequence){$('overview-'+name+'-error').textContent=error.message+' 已保留当前页。';$('overview-'+name+'-error').hidden=false;}}finally{if(sequence===p.sequence){p.busy=false;controls();}}}
  async function refreshOverview() {
    const sequence=++overviewSequence;
    try {const data=await post('/api/overview/refresh');if(sequence!==overviewSequence)return;counts(data);renderOverview(data);$('overview-error').hidden=true;serverState('服务器已连接');lastRefresh=Date.now();}
    catch(error) {if(sequence!==overviewSequence)return;$('overview-error').textContent=`${error.message} 已保留最近读取的数据，个人关注可继续使用。`;$('overview-error').className='inline-notice error';$('overview-error').hidden=false;serverState('服务器暂不可达',true);}
  }
  function context() {
    const state=views[kind],publicMode=state.scope==='public',creator=kind==='creator';
    if(publicMode&&state.sort==='added')state.sort='published';
    if(publicMode&&creator&&['views','published'].includes(state.sort))state.sort='popular';
    $('library-title').textContent=publicMode?(creator?'服务器公开 UP 库':'服务器公开视频库'):(creator?'我的 UP 主库':'我的视频库');
    $('library-description').textContent=publicMode?'搜索服务器已记录的目标；公开记录只展示服务器已有数据，关注后自动申请持续跟踪。':'个人关注保存在本机，先展示缓存，打开详情或刷新后从 B站和服务器更新资料。';
    $('scope-local').setAttribute('aria-pressed',String(!publicMode));$('scope-public').setAttribute('aria-pressed',String(publicMode));
    $('library-query').placeholder=creator?'搜索名称、UID 或 B站空间链接':'搜索标题、BV 号或 B站视频链接';
    $('library-sort').value=state.sort;
    $('library-sort').querySelector('[value="popular"]').textContent=creator?'粉丝数':'播放量';
    $('library-sort').querySelector('[value="views"]').hidden=!creator||publicMode;
    $('library-sort').querySelector('[value="published"]').hidden=creator&&publicMode;
    $('library-sort').querySelector('[value="added"]').hidden=publicMode;
    $('pending-filter-label').hidden=publicMode;$('pending-filter').checked=state.pending;
    $('library-source').textContent=publicMode?'按服务器已记录目标筛选和排序；当前资料本地查询':'个人关注保存在本机';
    if(state.sort==='hot')$('library-source').textContent+=' · 最近热门按近7日观测增量，缺少采样的排在末尾';
    if(creator&&state.sort==='views')$('library-source').textContent+=' · 播放量为本地已关注视频的播放合计';
    $('refresh-library').textContent=publicMode?'↻ 刷新搜索结果':'↻ 刷新本页资料';
    if(!publicMode)$('refresh-library').textContent='↻ 刷新库资料';
    summaryNote();
  }
  function summaryKey(base,k){const view=views[k];return base+'|'+summaryEpoch+'|'+k+'|'+view.page+'|'+view.q+'|'+view.sort+'|'+view.pending;}
  function summaryNote(){const text=summaryNotes.get(summaryKey(server,kind))||'';$('library-read-note').textContent=text;$('library-read-note').hidden=views[kind].scope==='public'||!text;}
  function syncLocalSummaries(requestedKind,base,force=false,requestedIDs=null){
    if(base!==server)return Promise.resolve(null);
    const noteKey=summaryKey(base,requestedKind),ids=requestedIDs||result.items.map(item=>item.id),key=noteKey+'|'+[...ids].sort().join(','),epoch=summaryEpoch;
    if(!ids.length)return Promise.resolve(null);
    if(summaryJobs.has(key))return summaryJobs.get(key);
    if(!force&&Date.now()-(summaryReads.get(key)||0)<60000)return Promise.resolve(null);
    summaryReads.set(key,Date.now());summaryNotes.set(noteKey,'正在更新本地资料与历史变化…');summaryNote();
    const job=(async()=>{
      try{if(!ids.length)return null;let data=null;for(let i=0;i<ids.length;i+=24){const chunk=ids.slice(i,i+24);const params=new URLSearchParams({kind:requestedKind});for(const id of chunk)params.append('id',id);data=await post('/api/follows/refresh?'+params);}summaryNotes.delete(noteKey);await localDashboard();return data;}
      catch(error){summaryNotes.set(noteKey,'资料更新未完成，已保留可用资料与本地关注。'+error.message);throw error;}
      finally{summaryJobs.delete(key);summaryNote();if(epoch===summaryEpoch&&server===base&&kind===requestedKind&&views[kind].scope==='local'&&current===(kind==='creator'?'creators':'videos')&&!$('follow-rows').querySelector('details[open]'))await load(false);}
    })();
    summaryJobs.set(key,job);return job;
  }
  function renderRows(data,publicMode) {renderCards({container:$('follow-rows'),data,kind,publicMode,button,images,onAdd:add,onRefresh:async f=>{await api('/api/library/'+f.kind+'/'+encodeURIComponent(f.id)+'?days=30&page=1');await load();},onRetry:async f=>{await submit(f);await reload();},onRemove:async f=>{await api(path(f),{method:'DELETE',body:'{}'});await reload();notify('已取消本地关注，服务器公共采集和历史保留。');},onScan});}
  async function load(sync=true) {
    if(!usable)return;
    const state=views[kind],requestedKind=kind,publicMode=state.scope==='public',sequence=++loadSequence;
    if(current===(requestedKind==='creator'?'creators':'videos')){const hash=`#${current}${publicMode?'?scope=public':''}`;history.replaceState(null,'',location.pathname+location.search+hash);currentRoute=routeForHash(hash);}
    context();$('library-loading').hidden=false;
    const query=new URLSearchParams({kind:requestedKind,q:state.q,page:String(state.page),sort:state.sort});
    if(!publicMode)query.set('limit',String(pageLimits[size]||20));
    if(state.pending&&!publicMode)query.set('pending','1');
    try {
      if(publicMode&&!server)throw new Error('请先在设置中配置服务器地址。');
      const data=await api(`${publicMode?'/api/remote/catalog':'/api/follows'}?${query}`);
      if(sequence!==loadSequence)return;
      result=data;state.page=data.page;renderRows(data,publicMode);if(!publicMode)lastLocalLoad=Date.now();
      $('library-page').textContent=`第 ${data.page} / ${Math.max(1,Math.ceil(data.total/data.limit))} 页 · 共 ${data.total} 项`;
      if(publicMode)serverState('服务器已连接');
      else if(sync)void syncLocalSummaries(requestedKind,server).catch(()=>{});
    }catch(error) {
      if(sequence!==loadSequence)return;
      result={items:[],page:1,limit:20,total:0};$('library-page').textContent='未能读取';
      const empty=node('div',null,'empty-copy');empty.append(node('p',error.message),button('前往设置',()=>navigate('settings')));$('follow-rows').replaceChildren(empty);
      if(publicMode&&server)serverState('服务器暂不可达',true);
    }finally{if(sequence===loadSequence){$('library-loading').hidden=true;controls();}}
  }
  async function reload() {await localDashboard();if(current==='creators'||current==='videos')await load();}
  async function run(action) {
    if(!usable||busy)return;busy=true;controls();
    try{await action();}catch(error){notify(error.message,true);await reload().catch(()=>{});}
    finally{busy=false;controls();}
  }
  async function submit(f) {
    await post(`${path(f)}/submit`);
    await post(`${path(f)}/refresh`).catch(error=>notify(`提交已确认，读取资料暂未完成：${error.message}`,true));
  }
  async function add(followKind,id) {
    const f=await api('/api/follows',{method:'POST',body:JSON.stringify({kind:followKind,id})});
    notify(f.remote?.tracking_state==='paused'||f.remote?.status==='paused'?'已保存本地关注，服务器已暂停此目标。':f.needs_attention?`已保存本地关注，需要手动核对。${f.last_error||''}`:f.pending_submission?`已保存本地关注，服务器尚未确认；后台将重试。${f.last_error||''}`:'已关注，服务器已确认持续跟踪。',Boolean(f.pending_submission));
    if(!f.pending_submission)await post(`${path(f)}/refresh`).catch(error=>notify(`已关注并提交，资料暂未读取：${error.message}`,true));
    summaryReads.clear();await reload();return f;
  }
  async function settings() {
    const data=await api('/api/settings');$('data-directory').value=data.data_directory;$('directory-current').textContent=data.data_directory;
    $('directory-bytes').textContent=`${(data.state_bytes/1024).toLocaleString('zh-CN',{maximumFractionDigits:1})} KB`;
  }
  $('server-form').addEventListener('submit',event=>{event.preventDefault();run(async()=>{
    const data=await api('/api/remote/connection',{method:'PUT',body:JSON.stringify({server_url:$('server-url').value.trim()})});
    server=data.server_url;summaryEpoch++;summaryReads.clear();summaryNotes.clear();$('server-url').value=server;$('server-note').textContent=server?`已保存 ${server}，待提交关注会自动申请跟踪。`:'未配置服务器，本地关注仍可保存。';
    overviewSequence++;lastRefresh=0;const overview=await localDashboard();renderOverview(overview);$('overview-error').hidden=true;
    serverState(server?'服务器已配置':'服务器未配置');notify('服务器配置已保存。');if(server)await refreshOverview();
  });});
  $('test-server').addEventListener('click',()=>run(async()=>{await post('/api/remote/test');serverState('服务器已连接');notify('服务器连接成功。');}));
  $('directory-form').addEventListener('submit',event=>{event.preventDefault();run(async()=>{
    if(!confirm('将现有关注、配置、扫描状态和受保护Cookie复制到新目录，并保留原目录备份。确认切换？'))return;
    await api('/api/settings/directory',{method:'PUT',body:JSON.stringify({data_directory:$('data-directory').value.trim()})});await settings();notify('工作目录已切换，重启后继续使用新位置，原数据已保留。');
  });});
  $('open-follow-dialog').addEventListener('click',()=>{dialogKind=kind;$('follow-dialog-title').textContent=kind==='creator'?'添加 UP 关注':'添加视频关注';$('follow-input-label').textContent=kind==='creator'?'UP 主 UID / 空间链接':'视频 BV 号 / 视频链接';$('follow-input').placeholder=kind==='creator'?'例如 600428973':'例如 BV17x411w7KC';$('follow-input').value='';$('follow-dialog').showModal();$('follow-input').focus();});
  $('follow-dialog-close').addEventListener('click',()=>$('follow-dialog').close());
  $('follow-form').addEventListener('submit',event=>{event.preventDefault();run(async()=>{await add(dialogKind,$('follow-input').value.trim());$('follow-dialog').close();});});
  $('scope-local').addEventListener('click',()=>run(async()=>{views[kind].scope='local';views[kind].page=1;await load();}));
  $('scope-public').addEventListener('click',()=>run(async()=>{views[kind].scope='public';views[kind].page=1;await load();}));
  $('library-search').addEventListener('submit',event=>{event.preventDefault();run(async()=>{views[kind].q=$('library-query').value.trim();views[kind].page=1;await load();});});
  $('library-sort').addEventListener('change',()=>run(async()=>{views[kind].sort=$('library-sort').value;views[kind].page=1;await load();}));
  $('pending-filter').addEventListener('change',()=>run(async()=>{views[kind].pending=$('pending-filter').checked;views[kind].page=1;await load();}));
  $('library-previous').addEventListener('click',()=>run(async()=>{views[kind].page--;await load();}));
  $('library-next').addEventListener('click',()=>run(async()=>{views[kind].page++;await load();}));
  $('refresh-library').addEventListener('click',()=>run(async()=>{
    if(views[kind].scope==='public'){await load();return;}
    await syncLocalSummaries(kind,server,true);await reload();notify('服务器已记录的库资料已刷新。');
  }));
  $('retry-pending').addEventListener('click',()=>run(async()=>{
    const pending=await api('/api/follows?pending=1');let confirmed=0;
    for(const f of pending.items.slice(0,5)){try{await submit(f);confirmed++;}catch{}}
    await reload();notify(`已确认 ${confirmed} 项；其余待提交项由后台继续重试。`);
  }));
  $('overview-refresh').addEventListener('click',()=>run(refreshOverview));
  for(const [k,name] of [['creator','creators'],['video','videos']]){for(const [direction,delta] of [['previous',-1],['next',1]])$('overview-'+name+'-'+direction).addEventListener('click',()=>overviewPage(k,overviewPages[k].page+delta));}
  const detail=initDetail({api,notify,onAdd:add,onChanged:localDashboard,onScan,onSyncSummaries:(k,ids)=>syncLocalSummaries(k,server,false,ids),images});
  let size='standard';try{const saved=localStorage.getItem('bht.card-size');if(['compact','standard','large'].includes(saved))size=saved;}catch{}
  function applyDensity(value){
    size=value;$('card-size').value=value;$('follow-rows').dataset.size=value;
    if(views[kind].scope==='local'&&(current==='creators'||current==='videos')){views[kind].page=1;void load();}
  }
  $('card-size').value=size;$('follow-rows').dataset.size=size;
  $('card-size').addEventListener('change',()=>{applyDensity($('card-size').value);try{localStorage.setItem('bht.card-size',size);}catch{}window.dispatchEvent(new CustomEvent('bht-density',{detail:size}));});
  window.addEventListener('bht-density',event=>{if(event.detail!==size)applyDensity(event.detail);});
  controls();
  return {
    setConnected(value){agentOnline=value;usable=value&&ready;detail.setConnected(usable);controls();},
    async connect(){ready=false;usable=false;detail.setConnected(false);controls();const data=await api('/api/remote/connection');server=data.server_url;$('server-url').value=server;$('server-note').textContent=server?`当前服务器：${server}`:'未配置服务器，本地关注仍可保存。';serverState(server?'服务器已配置':'服务器未配置');const overview=await localDashboard();renderOverview(overview);await settings();ready=true;usable=agentOnline;detail.setConnected(usable);controls();await this.show(current,currentRoute);},
    async show(view,route=routeForHash(location.hash)){current=view;currentRoute=route;if(view==='detail'){await detail.show(route);return;}detail.leave();if(!usable)return;if(view==='creators'||view==='videos'){kind=view==='creators'?'creator':'video';if(route.scope==='public')views[kind].scope='public';$('library-query').value=views[kind].q;await load();}else if(view==='overview'){const data=await localDashboard();renderOverview(data);if(server&&Date.now()-lastRefresh>60000)await refreshOverview();}else if(view==='settings')await settings();},
    async tick(){if(!usable||busy)return;if(current==='detail')await detail.tick();const data=await api('/api/overview');const changed=counts(data);if(current==='overview'&&!loadedOverview)renderOverview(data);if((changed||Date.now()-lastLocalLoad>8000)&&views[kind].scope==='local'&&(current==='creators'||current==='videos')&&!$('follow-dialog').open&&document.activeElement?.tagName!=='INPUT')await load();},
    add(followKind,id){return run(()=>add(followKind,id));}
  };
}
