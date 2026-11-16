<<<<<<< HEAD
// library.js 编排公开总览、本地库/公开搜索、自动关注与设置；业务请求经本地Agent，图片直连白名单CDN。
import {detailHref,routeForHash} from './view.mjs';
import {renderCards} from './cards.js';
import {initDetail} from './detail.js';
import {initImages} from './images.js';

export function initLibrary({api,notify,onScan,navigate}) {
  const $ = id => document.getElementById(id);
  const images=initImages({api});
  const views = {creator:{page:1,q:'',scope:'local',pending:false},video:{page:1,q:'',scope:'local',pending:false}};
  let usable=false,agentOnline=false,ready=false,busy=false,server='',kind='creator',current='overview',result={items:[],page:1,total:0,limit:20};
  let lastLocalLoad=0;
  let loadSequence=0,overviewSequence=0,dashboard=null,loadedOverview=false,lastRefresh=0;
  let dialogKind='creator',currentRoute=routeForHash(location.hash),overviewStamp='';
  const overviewPages={creator:{page:1,limit:18,total:0,items:[],busy:false,sequence:0},video:{page:1,limit:18,total:0,items:[],busy:false,sequence:0}};
=======
// library.js 编排公开总览、本地库/公开搜索、自动关注与设置；所有请求经本地Agent。
import {followState} from './view.mjs';

export function initLibrary({api,notify,onScan,navigate}) {
  const $ = id => document.getElementById(id);
  const views = {creator:{page:1,q:'',scope:'local',pending:false},video:{page:1,q:'',scope:'local',pending:false}};
  let usable=false,busy=false,server='',kind='creator',current='overview',result={items:[],page:1,total:0,limit:20};
  let lastLocalLoad=0;
  let loadSequence=0,overviewSequence=0,dashboard=null,loadedOverview=false,lastRefresh=0;
  let active=null,detailPage=1,detailTotal=0,detailLimit=10,detailSequence=0,dialogKind='creator';
>>>>>>> 692d77faa8f4b746bca16c153c39ad9b81cc6282
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
<<<<<<< HEAD
    for(const [k,name] of [['creator','creators'],['video','videos']]){const p=overviewPages[k];$('overview-'+name+'-previous').disabled=!usable||busy||p.busy||p.page<=1;$('overview-'+name+'-next').disabled=!usable||busy||p.busy||!server||p.page*p.limit>=p.total;}
=======
    $('detail-previous').disabled=!usable||busy||detailPage<=1;
    $('detail-next').disabled=!usable||busy||detailPage*detailLimit>=detailTotal;
    for(const el of $('library-detail').querySelectorAll('.version-button'))el.disabled=!usable||busy;
  }
  function counts(value) {
    const oldPending=dashboard?.pending;dashboard=value;server=value.server_url;
    $('creator-library-count').textContent=value.creators;$('video-library-count').textContent=value.videos;
    $('local-creators').textContent=value.creators;$('local-videos').textContent=value.videos;$('local-pending').textContent=value.pending;
    controls();return oldPending!==undefined&&oldPending!==value.pending;
  }
  async function localDashboard() {const data=await api('/api/overview');counts(data);return data;}
  function compact(items,container,empty) {
    const rows=document.createDocumentFragment();
    for(const item of items||[]) {const row=node('div',null,'compact-item');const info=node('div',null,'compact-info');info.append(node('strong',item.name||item.id),node('span',`${item.kind==='creator'?'粉丝':'播放'} ${number(item.metric)} · ${item.id}`));row.append(node('span',(item.name||item.id).slice(0,1),'avatar'),info,button('查看',()=>openDetail(item),'text-button'));rows.append(row);}
    if(!items?.length)rows.append(node('p',empty,'empty-copy'));$(container).replaceChildren(rows);
  }
  function renderOverview(value) {
    const data=value.public;
    for(const [field,id] of [['creators','public-creators'],['videos','public-videos'],['tracking','public-tracking'],['updates','public-updates']])$(id).textContent=data?number(data.summary[field]):'—';
    $('overview-time').textContent=data?`上次读取 ${date(data.fetched_at)}`:'尚未读取';
    compact(data?.creators,'overview-creators',server?'暂无已读取的公开 UP 资料。':'在设置中配置服务器后查看公开资料。');
    compact(data?.videos,'overview-videos',server?'暂无已读取的公开视频。':'公开记录由你连接的服务器提供。');
    const rows=document.createDocumentFragment();
    for(const update of data?.updates||[]) {const row=node('div',null,'update-row'),info=node('div');info.append(node('strong',update.name||update.id),node('small',`${update.kind==='creator'?'UP':'视频'} ${update.id} · 版本 ${update.seq}`));row.append(node('span',update.kind==='creator'?'◎':'▷','avatar'),info,node('time',date(update.at)));rows.append(row);}
    if(!data?.updates?.length)rows.append(node('p','暂无已读取的版本动态。','empty-copy'));$('overview-updates').replaceChildren(rows);loadedOverview=true;
  }
  async function refreshOverview() {
    const sequence=++overviewSequence;
    try {const data=await post('/api/overview/refresh');if(sequence!==overviewSequence)return;counts(data);renderOverview(data);$('overview-error').hidden=true;serverState('服务器已连接');lastRefresh=Date.now();}
    catch(error) {if(sequence!==overviewSequence)return;$('overview-error').textContent=`${error.message} 已保留最近读取的数据，个人关注可继续使用。`;$('overview-error').className='inline-notice error';$('overview-error').hidden=false;serverState('服务器暂不可达',true);}
  }
  function context() {
    const state=views[kind],publicMode=state.scope==='public',creator=kind==='creator';
    $('library-title').textContent=publicMode?(creator?'服务器公开 UP 库':'服务器公开视频库'):(creator?'我的 UP 主库':'我的视频库');
    $('library-description').textContent=publicMode?'仅搜索服务器已经收录的公开记录，关注后自动申请持续跟踪。':'个人关注保存在本机，资料来自服务器最近一次观测。';
    $('scope-local').setAttribute('aria-pressed',String(!publicMode));$('scope-public').setAttribute('aria-pressed',String(publicMode));
    $('library-query').placeholder=`搜索${publicMode?'服务器记录':'本地关注'}的名称或${creator?' UID':' BV 号'}`;
    $('pending-filter-label').hidden=publicMode;$('pending-filter').checked=state.pending;
    $('library-source').textContent=publicMode?'来源：服务器公开库，不代表 B站全站搜索。未收录的目标可按 UID/BV 添加。':'来源：本地关注；服务器状态和本地关注分别保存。';
    $('refresh-library').textContent=publicMode?'↻ 刷新搜索结果':'↻ 刷新本页资料';
  }
  function renderRows(data,publicMode) {
    const rows=document.createDocumentFragment();
    for(const f of data.items) {
      const item=publicMode?f:f.data?.item;
      const followed=publicMode?f.followed:true;
      const row=node('article',null,'follow-row'),info=node('div',null,'follow-info');
      info.append(node('strong',item?.name||f.id),node('span',`${kind==='creator'?'UP · UID':'视频'} ${f.id}`));
      info.append(node('p',`${kind==='creator'?'粉丝':'播放'} ${number(item?.metric)} · 最近7天净变化 ${number(item?.growth)} · 版本 ${number(item?.seq)}`));
      if(publicMode)info.append(node('span',followed?'已本地关注':'尚未本地关注','follow-status'));
      else {
        info.append(node('span',`已本地关注 · ${followState(f)}`,'follow-status'));
        if(f.updated_at)info.append(node('small',`上次核对 ${date(f.updated_at)}${item?` · 数据观测 ${date(item.observed_at)}`:''}`));
        if(f.last_error)info.append(node('p',`最近提交/读取：${f.last_error}`,'follow-error'));
      }
      const actions=node('div',null,'follow-actions');
      if(!followed)actions.append(button('关注并跟踪',()=>add(f.kind,f.id),'button primary'));
      if(publicMode||item)actions.append(button('详情与历史',()=>openDetail({kind:f.kind,id:f.id})));
      if(!publicMode) {
        actions.append(button('刷新资料',async()=>{await post(`${path(f)}/refresh`);await load();}));
        if(f.pending_submission||!f.remote?.tracking)actions.append(button('重试提交',async()=>{await submit(f);await reload();}));
        if(kind==='creator')actions.append(button('获取全部视频',()=>onScan(f.id)));
        actions.append(button('取消本地关注',async()=>{await api(path(f),{method:'DELETE',body:'{}'});await reload();notify('已取消本地关注，服务器公共跟踪和历史保留。');},'text-button danger'));
      }
      row.append(node('span',(item?.name||f.id).slice(0,1),'avatar'),info,actions);rows.append(row);
    }
    if(!data.items.length) {
      const empty=node('div',null,'empty-copy');empty.append(node('p',publicMode?'服务器暂无匹配记录。可按 UID 或 BV 号直接添加关注，由服务器验证。':'暂无匹配的本地关注。可以搜索服务器公开库，或按 UID/BV 添加。'));
      if(!publicMode)empty.append(button('浏览服务器公开库',async()=>{views[kind].scope='public';views[kind].page=1;await load();}));
      rows.append(empty);
    }
    $('follow-rows').replaceChildren(rows);
>>>>>>> 692d77faa8f4b746bca16c153c39ad9b81cc6282
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
    for(const item of items||[]) {const row=node('div',null,'compact-item');const info=node('div',null,'compact-info');info.append(node('strong',item.name||item.id),node('span',`${item.kind==='creator'?'粉丝':'播放'} ${number(item.metric)} · ${item.id}`));const avatar=node('span',(item.name||item.id).slice(0,1),'avatar');images.show(avatar,{kind:item.kind,id:item.id,seq:item.seq});row.append(avatar,info);const link=node('a','查看 →','text-button');link.href=detailHref(item.kind,item.id,'public');row.append(link);rows.append(row);}
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
    $('library-title').textContent=publicMode?(creator?'服务器公开 UP 库':'服务器公开视频库'):(creator?'我的 UP 主库':'我的视频库');
    $('library-description').textContent=publicMode?'仅搜索服务器已经收录的公开记录，关注后自动申请持续跟踪。':'个人关注保存在本机，资料来自服务器最近一次观测。';
    $('scope-local').setAttribute('aria-pressed',String(!publicMode));$('scope-public').setAttribute('aria-pressed',String(publicMode));
    $('library-query').placeholder=`搜索${publicMode?'服务器记录':'本地关注'}的名称或${creator?' UID':' BV 号'}`;
    $('pending-filter-label').hidden=publicMode;$('pending-filter').checked=state.pending;
    $('library-source').textContent=publicMode?'来源：服务器公开库，不代表 B站全站搜索。未收录的目标可按 UID/BV 添加。':'来源：本地关注；服务器状态和本地关注分别保存。';
    $('refresh-library').textContent=publicMode?'↻ 刷新搜索结果':'↻ 刷新本页资料';
  }
  function renderRows(data,publicMode) {renderCards({container:$('follow-rows'),data,kind,publicMode,button,images,onAdd:add,onRefresh:async f=>{await post(path(f)+'/refresh');await load();},onRetry:async f=>{await submit(f);await reload();},onRemove:async f=>{await api(path(f),{method:'DELETE',body:'{}'});await reload();notify('已取消本地关注，服务器公共采集和历史保留。');},onScan});}
  async function load() {
    if(!usable)return;
    const state=views[kind],requestedKind=kind,publicMode=state.scope==='public',sequence=++loadSequence;
<<<<<<< HEAD
    if(current===(requestedKind==='creator'?'creators':'videos')){const hash=`#${current}${publicMode?'?scope=public':''}`;history.replaceState(null,'',location.pathname+location.search+hash);currentRoute=routeForHash(hash);}
=======
>>>>>>> 692d77faa8f4b746bca16c153c39ad9b81cc6282
    context();$('library-loading').hidden=false;
    const query=new URLSearchParams({kind:requestedKind,q:state.q,page:String(state.page)});
    if(state.pending&&!publicMode)query.set('pending','1');
    try {
      if(publicMode&&!server)throw new Error('请先在设置中配置服务器地址。');
      const data=await api(`${publicMode?'/api/remote/catalog':'/api/follows'}?${query}`);
      if(sequence!==loadSequence)return;
      result=data;state.page=data.page;renderRows(data,publicMode);if(!publicMode)lastLocalLoad=Date.now();
      $('library-page').textContent=`第 ${data.page} / ${Math.max(1,Math.ceil(data.total/data.limit))} 页 · 共 ${data.total} 项`;
      if(publicMode)serverState('服务器已连接');
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
    notify(f.pending_submission?`已保存本地关注，服务器尚未确认；后台将重试。${f.last_error||''}`:'已关注，服务器已确认持续跟踪。',Boolean(f.pending_submission));
    if(!f.pending_submission)await post(`${path(f)}/refresh`).catch(error=>notify(`已关注并提交，资料暂未读取：${error.message}`,true));
    await reload();return f;
  }
<<<<<<< HEAD
  async function settings() {
    const data=await api('/api/settings');$('data-directory').value=data.data_directory;$('directory-current').textContent=data.data_directory;
    $('directory-bytes').textContent=`${(data.state_bytes/1024).toLocaleString('zh-CN',{maximumFractionDigits:1})} KB`;
  }
=======
  async function readDetail(seq=0) {
    const sequence=++detailSequence,query=new URLSearchParams({page:String(detailPage)});if(seq)query.set('seq',String(seq));
    const d=await api(`/api/library/${active.kind}/${encodeURIComponent(active.id)}?${query}`);
    if(sequence!==detailSequence||!$('library-detail').open)return;
    detailPage=d.version_page;detailTotal=d.version_total;detailLimit=d.version_limit;
    $('detail-title').textContent=d.item.name||active.id;$('detail-summary').textContent=`${active.id} · ${active.kind==='creator'?'粉丝':'播放'} ${number(d.item.metric)} · 观测于 ${date(d.item.observed_at)}`;
    const versions=document.createDocumentFragment();
    for(const v of d.versions||[])versions.append(button(`版本 ${v.seq} · ${date(v.at)} · ${v.name}`,()=>readDetail(v.seq),'version-button'));
    $('detail-versions').replaceChildren(versions);$('detail-version-page').textContent=`${detailPage} / ${Math.max(1,Math.ceil(detailTotal/detailLimit))} · ${detailTotal} 个版本`;
    $('detail-snapshot').textContent=d.snapshot?JSON.stringify(d.snapshot,null,2):'点击版本查看服务器保存的元数据快照。';
    const points=document.createDocumentFragment();for(const p of d.metrics.points||[]) {const row=node('tr');row.append(node('td',date(p.at)),node('td',number(p.value)));points.append(row);}
    $('detail-metrics').replaceChildren(points);$('detail-metric-note').textContent=d.metrics.points?.length?`${d.metrics.points.length} 个真实日采样（最近30天）`:'暂无日采样，历史不补造。';controls();
  }
  async function openDetail(f) {
    active=f;detailPage=1;detailTotal=0;$('detail-title').textContent='正在读取详情…';$('detail-summary').textContent=f.id;
    $('detail-versions').replaceChildren();$('detail-metrics').replaceChildren();$('detail-snapshot').textContent='';$('detail-metric-note').textContent='';
    $('library-detail').showModal();try{await readDetail();}catch(error){$('detail-summary').textContent=error.message;throw error;}
  }
  async function settings() {
    const data=await api('/api/settings');$('data-directory').value=data.data_directory;$('directory-current').textContent=data.data_directory;
    $('directory-bytes').textContent=`${(data.state_bytes/1024).toLocaleString('zh-CN',{maximumFractionDigits:1})} KB`;
  }
>>>>>>> 692d77faa8f4b746bca16c153c39ad9b81cc6282
  $('server-form').addEventListener('submit',event=>{event.preventDefault();run(async()=>{
    const data=await api('/api/remote/connection',{method:'PUT',body:JSON.stringify({server_url:$('server-url').value.trim()})});
    server=data.server_url;$('server-url').value=server;$('server-note').textContent=server?`已保存 ${server}，待提交关注会自动申请跟踪。`:'未配置服务器，本地关注仍可保存。';
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
  $('pending-filter').addEventListener('change',()=>run(async()=>{views[kind].pending=$('pending-filter').checked;views[kind].page=1;await load();}));
  $('library-previous').addEventListener('click',()=>run(async()=>{views[kind].page--;await load();}));
  $('library-next').addEventListener('click',()=>run(async()=>{views[kind].page++;await load();}));
  $('refresh-library').addEventListener('click',()=>run(async()=>{
    if(views[kind].scope==='public'){await load();return;}
    let done=0;for(const f of result.items){$('library-loading').hidden=false;$('library-loading').textContent=`正在刷新 ${++done}/${result.items.length}…`;await post(`${path(f)}/refresh`);}
    await reload();notify('本页服务器资料已刷新。');
  }));
  $('retry-pending').addEventListener('click',()=>run(async()=>{
    const pending=await api('/api/follows?pending=1');let confirmed=0;
    for(const f of pending.items.slice(0,5)){try{await submit(f);confirmed++;}catch{}}
    await reload();notify(`已确认 ${confirmed} 项；其余待提交项由后台继续重试。`);
  }));
  $('overview-refresh').addEventListener('click',()=>run(refreshOverview));
<<<<<<< HEAD
  for(const [k,name] of [['creator','creators'],['video','videos']]){for(const [direction,delta] of [['previous',-1],['next',1]])$('overview-'+name+'-'+direction).addEventListener('click',()=>overviewPage(k,overviewPages[k].page+delta));}
  const detail=initDetail({api,notify,onAdd:add,onChanged:localDashboard,onScan,images});
  let size='standard';try{const saved=localStorage.getItem('bht.card-size');if(['compact','standard','large'].includes(saved))size=saved;}catch{}
  $('card-size').value=size;$('follow-rows').dataset.size=size;
  $('card-size').addEventListener('change',()=>{size=$('card-size').value;$('follow-rows').dataset.size=size;try{localStorage.setItem('bht.card-size',size);}catch{}});
  controls();
  return {
    setConnected(value){agentOnline=value;usable=value&&ready;detail.setConnected(usable);controls();},
    async connect(){ready=false;usable=false;detail.setConnected(false);controls();const data=await api('/api/remote/connection');server=data.server_url;$('server-url').value=server;$('server-note').textContent=server?`当前服务器：${server}`:'未配置服务器，本地关注仍可保存。';serverState(server?'服务器已配置':'服务器未配置');const overview=await localDashboard();renderOverview(overview);await settings();ready=true;usable=agentOnline;detail.setConnected(usable);controls();await this.show(current,currentRoute);},
    async show(view,route=routeForHash(location.hash)){current=view;currentRoute=route;if(view==='detail'){await detail.show(route);return;}detail.leave();if(!usable)return;if(view==='creators'||view==='videos'){kind=view==='creators'?'creator':'video';if(route.scope==='public')views[kind].scope='public';$('library-query').value=views[kind].q;await load();}else if(view==='overview'){const data=await localDashboard();renderOverview(data);if(server&&Date.now()-lastRefresh>60000)await refreshOverview();}else if(view==='settings')await settings();},
    async tick(){if(!usable||busy)return;const data=await api('/api/overview');const changed=counts(data);if(current==='overview'&&!loadedOverview)renderOverview(data);if((changed||Date.now()-lastLocalLoad>8000)&&views[kind].scope==='local'&&(current==='creators'||current==='videos')&&!$('follow-dialog').open&&document.activeElement?.tagName!=='INPUT')await load();},
=======
  $('detail-close').addEventListener('click',()=>$('library-detail').close());$('library-detail').addEventListener('close',()=>{detailSequence++;});
  $('detail-previous').addEventListener('click',()=>run(async()=>{detailPage--;await readDetail();}));$('detail-next').addEventListener('click',()=>run(async()=>{detailPage++;await readDetail();}));
  controls();
  return {
    setConnected(value){usable=value;controls();},
    async connect(){const data=await api('/api/remote/connection');server=data.server_url;$('server-url').value=server;$('server-note').textContent=server?`当前服务器：${server}`:'未配置服务器，本地关注仍可保存。';serverState(server?'服务器已配置':'服务器未配置');const overview=await localDashboard();renderOverview(overview);await settings();await this.show(current);},
    async show(view){current=view;if(!usable)return;if(view==='creators'||view==='videos'){kind=view==='creators'?'creator':'video';$('library-query').value=views[kind].q;await load();}else if(view==='overview'){const data=await localDashboard();renderOverview(data);if(server&&Date.now()-lastRefresh>60000)await refreshOverview();}else if(view==='settings')await settings();},
    async tick(){if(!usable||busy)return;const data=await api('/api/overview');const changed=counts(data);if(current==='overview'&&!loadedOverview)renderOverview(data);if((changed||Date.now()-lastLocalLoad>8000)&&views[kind].scope==='local'&&(current==='creators'||current==='videos')&&!$('follow-dialog').open&&!$('library-detail').open&&document.activeElement?.tagName!=='INPUT')await load();},
>>>>>>> 692d77faa8f4b746bca16c153c39ad9b81cc6282
    add(followKind,id){return run(()=>add(followKind,id));}
  };
}
