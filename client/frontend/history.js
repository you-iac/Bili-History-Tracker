// history.js 在会话就绪后先读本地缓存、自动同步；筛选和翻页不重复请求平台。
// 卡片进入站内详情，关注由用户显式触发；图片复用白名单与失败占位。见 docs/client-catalog.md。
import {compactNumber,detailHref,cardPageLimits} from './view.mjs';
import {initImages} from './images.js';

export function initHistory({api,notify}) {
  const $=id=>document.getElementById(id),images=initImages({api}),follows=new Map();
  const node=(tag,text,className)=>{const el=document.createElement(tag);if(text!=null)el.textContent=text;if(className)el.className=className;return el;};
  const date=value=>value?new Date(value*1000).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'时间未知';
  const autoInterval=5*60*1000;
  let size='standard',unfollowed=false;
  try{const saved=localStorage.getItem('bht.card-size');if(Object.hasOwn(cardPageLimits,saved))size=saved;}catch{}
  let kind='video',days=30,page=1,result={items:[],page:1,limit:20,total:0};
  let usable=false,configured=false,visible=false,loading=false,syncing=false,interacting=false,sequence=0,lastAttempt=0;
  let syncMessage='',syncError='',readError='',emptyDescription=null;
  const emptyCopy=()=>syncing?'正在同步，记录将在同步完成后显示。':unfollowed?'可以关闭「仅看未关注」查看全部记录，或调整观看范围。':configured?'可以扩大时间范围，或点击「同步历史」获取最新记录。':'在设置中配置 B站 Cookie，便可自动导入观看历史。';
  function controls(){
    const busy=!usable||!configured||syncing||interacting;
    $('history-sync').disabled=busy;
    $('history-sync').textContent=syncing?'正在同步…':'↻ 同步历史';
    $('history-interactions').disabled=busy;
    $('history-interactions').textContent=interacting?'检查中…':'检查互动';
    $('history-days').disabled=!usable;
    $('history-card-size').disabled=!usable;$('history-unfollowed').disabled=!usable;
    $('history-unfollowed').setAttribute('aria-pressed',String(unfollowed));
    for(const value of ['video','creator']){
      const tab=$('history-'+value+'s-tab');tab.disabled=!usable;tab.setAttribute('aria-pressed',String(kind===value));
    }
    $('history-previous').disabled=!usable||loading||page<=1;
    $('history-next').disabled=!usable||loading||page*result.limit>=result.total;
    $('history-rows').setAttribute('aria-busy',String(loading));$('history-loading').hidden=!loading;
    $('history-state').textContent=!usable?'连接 Agent 后自动读取和同步历史。':readError||syncError||
      (!configured?'配置 B站 Cookie 后，进入此页即可自动同步。':syncing?'正在同步最近 30 天的观看历史，你可以继续浏览本地记录。':syncMessage||'进入页面自动同步，历史只保存在本机。');
    $('history-state').classList.toggle('history-error',Boolean(readError||syncError));
    $('history-settings').hidden=!usable||(configured&&!syncError);$('history-retry').hidden=!usable||!readError;
    if(emptyDescription)emptyDescription.textContent=emptyCopy();
  }
  function followState(button,key,fallback=false){
    const state=follows.get(key);button.disabled=!usable||Boolean(state)||fallback;
    button.textContent=state==='working'?'关注中…':state==='pending'?'已关注 · 待提交':state||fallback?'已关注':'＋ 关注';
    button.className=`button ${state&&state!=='working'||fallback?'secondary':'primary'} small`;
  }
  function updateFollowButtons(){
    for(const button of $('history-rows').querySelectorAll('[data-history-follow]'))followState(button,button.dataset.historyFollow,button.dataset.followed==='true');
  }
  async function followItem(targetKind,id){
    const key=`${targetKind}:${id}`;if(!usable||follows.has(key))return;
    follows.set(key,'working');updateFollowButtons();
    try{
      const data=await api('/api/follows',{method:'POST',body:JSON.stringify({kind:targetKind,id})});
      follows.set(key,data.pending_submission?'pending':'followed');
      notify(data.needs_attention?'已保存本地关注，需要手动核对。'+(data.last_error||''):data.pending_submission?'已保存本地关注，服务器尚未确认，后台将重试。':'已保存本地关注。',Boolean(data.needs_attention||data.pending_submission));
      if(unfollowed&&visible)await load();
    }catch(error){follows.delete(key);notify(error.message,true);}
    finally{updateFollowButtons();}
  }
  function actionButton(targetKind,id,followed){
    const button=node('button');button.type='button';button.dataset.historyFollow=`${targetKind}:${id}`;button.dataset.followed=String(Boolean(followed));
    button.setAttribute('aria-label',`关注${targetKind==='creator'?' UP 主':'视频'} ${id}`);followState(button,button.dataset.historyFollow,followed);
    button.addEventListener('click',()=>followItem(targetKind,id));return button;
  }
  function link(text,href,className,external=false){
    const el=node('a',text,className);el.href=href;if(external){el.target='_blank';el.rel='noopener noreferrer';}return el;
  }
  function card(item){
    const creator=kind==='creator',id=creator?item.id:item.bvid,name=(creator?item.name:item.title)||id;
    const article=node('article',null,`entity-card watch-history-card${creator?' watch-history-creator':''}`);
    const href=detailHref(kind,id),media=link(null,href,creator?'history-avatar-link':'history-cover-link');
    media.setAttribute('aria-label',`查看${creator?' UP 主':'视频'}详情：${name}`);
    const avatar=node('span',creator?name.slice(0,1):'▷',creator?'entity-avatar history-avatar':'history-cover');
    // 历史聚合不保证含头像；缺失时按可见区域补取，避免未缓存的 UP 永久停在首字占位。
    images.show(avatar,{kind,id,url:item.cover||'',resolve:creator});media.append(avatar);
    const title=link(name,href,'history-title');title.title=name;
    const info=node('div',null,'history-info');info.append(title);
    if(creator)info.append(node('span',`UID ${id}`,'history-id'));
    else if(item.author_id)info.append(link(item.author_name||`UP 主 ${item.author_id}`,detailHref('creator',item.author_id),'history-author'));
    info.append(node('p',creator?`看过 ${compactNumber(item.videos)} 个视频`:item.bvid,'history-meta'));
    const timestamp=creator?item.last_viewed_at:item.viewed_at,time=node('time',`${creator?'最近观看':'观看于'} ${date(timestamp)}`,'history-time');
    if(timestamp)time.dateTime=new Date(timestamp*1000).toISOString();info.append(time);
    if(!creator){
      const badges=[];if(item.like)badges.push('已赞');if(item.coin>0)badges.push(`投币 ${item.coin}`);if(item.favorite)badges.push('已收藏');
      if(badges.length)info.append(node('p',badges.join(' · '),'history-badges'));
    }
    const actions=node('div',null,'history-actions');
    actions.append(link(creator?'B站主页 ↗':'B站播放 ↗',creator?`https://space.bilibili.com/${encodeURIComponent(id)}`:`https://www.bilibili.com/video/${encodeURIComponent(id)}`,'text-button',true),actionButton(kind,id,item.followed));
    article.append(media,info,actions);
    article.addEventListener('click',event=>{if(!event.target.closest('a,button')&&!window.getSelection()?.toString())title.click();});return article;
  }
  function render(){
    const rows=$('history-rows'),fragment=document.createDocumentFragment();rows.dataset.kind=kind;emptyDescription=null;
    for(const item of result.items)fragment.append(card(item));
    if(!result.items.length){
      const empty=node('div',null,'history-empty');
      emptyDescription=node('p',emptyCopy());
      empty.append(node('span','◷','history-empty-icon'),node('strong',`最近 ${days} 天暂无${unfollowed?'未关注的':''}${kind==='video'?'观看记录':'观看过的 UP 主'}`),emptyDescription);fragment.append(empty);
    }
    rows.replaceChildren(fragment);
    $('history-page').textContent=`第 ${result.page} / ${Math.max(1,Math.ceil(result.total/result.limit))} 页 · 共 ${result.total} ${kind==='video'?'个视频':'位 UP 主'}${unfollowed?'（未关注）':''} · 每页 ${result.limit} 项`;
    $('history-context').textContent=kind==='video'?'按最近观看排序 · 点击卡片查看详情，点击 UP 名称查看作者。':'按最近观看排序 · 关注 UP 主只采集作者资料，不会自动关注其视频。';
  }
  async function load(){
    if(!usable)return;const current=++sequence;loading=true;readError='';controls();
    try{
      const data=await api(`/api/history/${kind==='video'?'videos':'creators'}?days=${days}&page=${page}&limit=${cardPageLimits[size]}${unfollowed?'&unfollowed=1':''}`);
      if(current!==sequence)return;result=data;page=data.page;render();
    }catch(error){if(current===sequence)readError=`读取历史失败：${error.message} 已保留当前列表。`;}
    finally{if(current===sequence){loading=false;controls();}}
  }
  async function sync(){
    if(!usable||!configured||syncing)return;syncing=true;lastAttempt=Date.now();syncError='';controls();
    try{
      const data=await api('/api/history/sync',{method:'POST',body:'{}'});
      syncMessage=`同步完成 · ${new Date(data.synced_at).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit',timeZone:'Asia/Shanghai'})} · 新增 ${data.added}，更新 ${data.updated}`;
      if(visible)await load();
    }catch(error){syncError=`同步未完成：${error.message} 本地历史仍可浏览。`;}
    finally{syncing=false;controls();}
  }
  async function interactions(){
    if(!usable||!configured||interacting)return;
    interacting=true;syncError='';controls();
    try{
      const data=await api('/api/history/interactions',{method:'POST',body:'{}'});
      syncMessage=`互动检查完成 · 已检查 ${data.checked} 条 · 点赞 ${data.liked} · 投币 ${data.coined} · 收藏 ${data.favorited}`;
      if(visible)await load();
    }catch(error){syncError=`互动检查未完成：${error.message}`;}
    finally{interacting=false;controls();}
  }
  function show(){
    visible=true;if(!usable){controls();return Promise.resolve();}
    for(const [key,state] of follows)if(state!=='working')follows.delete(key);
    // 缓存与同步并行，序号阻止旧页响应覆盖同步结果或新筛选。
    return Promise.all([load(),configured&&(!lastAttempt||Date.now()-lastAttempt>=autoInterval)?sync():undefined]);
  }
  function applyDensity(value){
    if(!Object.hasOwn(cardPageLimits,value)||value===size)return;
    size=value;$('history-card-size').value=size;$('history-rows').dataset.size=size;page=1;
    // 隐藏页也接收共享设置；使旧请求失效，返回时再按新数量读取。
    sequence++;loading=false;controls();if(visible&&usable)void load();
  }
  $('history-card-size').value=size;$('history-rows').dataset.size=size;
  $('history-card-size').addEventListener('change',()=>{
    applyDensity($('history-card-size').value);
    try{localStorage.setItem('bht.card-size',size);}catch{}
    window.dispatchEvent(new CustomEvent('bht-density',{detail:size}));
  });
  window.addEventListener('bht-density',event=>applyDensity(event.detail));
  $('history-unfollowed').addEventListener('click',()=>{unfollowed=!unfollowed;page=1;load();});
  $('history-sync').addEventListener('click',sync);$('history-interactions').addEventListener('click',interactions);$('history-retry').addEventListener('click',load);
  $('history-days').addEventListener('change',()=>{days=Number($('history-days').value);page=1;load();});
  for(const value of ['video','creator'])$('history-'+value+'s-tab').addEventListener('click',()=>{if(kind===value)return;kind=value;page=1;load();});
  $('history-previous').addEventListener('click',()=>{if(page>1){page--;load();}});
  $('history-next').addEventListener('click',()=>{if(page*result.limit<result.total){page++;load();}});
  controls();
  return {
    show,leave(){visible=false;},
    setConnected(value,cookieConfigured){
      const changed=usable!==value||configured!==Boolean(cookieConfigured);
      if(configured!==Boolean(cookieConfigured)){lastAttempt=0;syncMessage='';syncError='';}
      usable=value;configured=Boolean(cookieConfigured);controls();updateFollowButtons();
      if(changed&&usable&&visible)void show();
    }
  };
}
