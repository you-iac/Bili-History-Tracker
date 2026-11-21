// history.js 管理观看历史页面：同步、3/7/30 天筛选、视频/UP 列表与关注。
// 历史只读本地缓存；关注仍走现有单条 /api/follows。见 docs/client-catalog.md。
import {compactNumber} from './view.mjs';

export function initHistory({api,notify}) {
  const $=id=>document.getElementById(id);
  let kind='video',days=30,page=1,result={items:[],page:1,limit:20,total:0},loading=false;
  const date=value=>value?new Date(value*1000).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'未知';
  const endpoint=()=>kind==='video'?'/api/history/videos':'/api/history/creators';
  function controls(){
    for(const id of ['history-sync','history-days','history-videos-tab','history-creators-tab'])$(id).disabled=loading;
    $('history-previous').disabled=loading||page<=1;
    $('history-next').disabled=loading||page*result.limit>=result.total;
  }
  async function followItem(targetKind,id,button){
    button.disabled=true;button.textContent='关注中…';
    try{await api('/api/follows',{method:'POST',body:JSON.stringify({kind:targetKind,id})});button.textContent='已关注';notify(`已关注 ${id}`);}
    catch(error){button.disabled=false;button.textContent='关注';notify(error.message,true);}
  }
  function actionButton(targetKind,id,label){
    const button=document.createElement('button');button.type='button';button.className='button primary small';button.textContent=label;
    button.addEventListener('click',()=>followItem(targetKind,id,button));return button;
  }
  function card(item){
    const article=document.createElement('article');article.className='entity-card video-card';
    const top=document.createElement('div');top.className='entity-top video-top';
    const avatar=document.createElement('span');avatar.className='entity-avatar video-placeholder';
    if(item.cover){const img=document.createElement('img');img.src=item.cover;img.alt='';img.loading='lazy';img.referrerPolicy='no-referrer';img.style.cssText='width:100%;height:100%;object-fit:cover;border-radius:inherit';avatar.append(img);}
    else avatar.textContent='▷';
    top.append(avatar);
    const title=document.createElement('a');title.className='entity-title';title.href=`https://www.bilibili.com/video/${item.bvid}`;title.target='_blank';title.rel='noopener noreferrer';title.textContent=item.title||item.bvid;
    const identity=document.createElement('p');identity.className='entity-id';identity.textContent=`${item.bvid}${item.author_name?' · '+item.author_name:''}`;
    const meta=document.createElement('p');meta.className='entity-basic';meta.textContent=`观看于 ${date(item.viewed_at)}`;
    const actions=document.createElement('div');actions.className='entity-actions';actions.append(actionButton('video',item.bvid,'关注'));
    article.append(top,title,identity,meta,actions);return article;
  }
  function creatorCard(item){
    const article=document.createElement('article');article.className='entity-card creator-card';
    const top=document.createElement('div');top.className='entity-top';
    const avatar=document.createElement('span');avatar.className='entity-avatar';avatar.textContent=(item.name||item.id).slice(0,1);top.append(avatar);
    const title=document.createElement('a');title.className='entity-title';title.href=`https://space.bilibili.com/${item.id}`;title.target='_blank';title.rel='noopener noreferrer';title.textContent=item.name||item.id;
    const identity=document.createElement('p');identity.className='entity-id';identity.textContent=`UID ${item.id}`;
    const meta=document.createElement('p');meta.className='entity-basic';meta.textContent=`观看 ${compactNumber(item.videos)} 个视频 · 最近 ${date(item.last_viewed_at)}`;
    const actions=document.createElement('div');actions.className='entity-actions';actions.append(actionButton('creator',item.id,'关注'));
    article.append(top,title,identity,meta,actions);return article;
  }
  async function load(){
    loading=true;controls();$('history-state').textContent='正在读取历史…';
    try{
      const data=await api(`${endpoint()}?days=${days}&page=${page}&limit=20`);
      result=data;page=data.page;
      const rows=$('history-rows');rows.replaceChildren();
      if(!data.items.length){const empty=document.createElement('p');empty.className='empty-copy';empty.textContent=kind==='video'?'暂无观看记录，先同步观看历史。':'暂无观看过的 UP，先同步观看历史。';rows.append(empty);}
      for(const item of data.items)rows.append(kind==='video'?card(item):creatorCard(item));
      $('history-page').textContent=`第 ${data.page} / ${Math.max(1,Math.ceil(data.total/data.limit))} 页 · 共 ${data.total} 项`;
      $('history-state').textContent=kind==='video'?'选择视频后关注；历史只保存在本机。':'按最近观看时间排序；关注后只采集作者资料。';
    }catch(error){$('history-state').textContent=error.message;}
    finally{loading=false;controls();}
  }
  async function sync(){
    if(loading)return;loading=true;controls();$('history-state').textContent='正在同步 B站观看历史…';
    try{
      const data=await api('/api/history/sync',{method:'POST',body:'{}'});
      notify(`历史已同步：新增 ${data.added}，更新 ${data.updated}，清理 ${data.removed}`);
      page=1;await load();
    }catch(error){$('history-state').textContent=error.message;}
    finally{loading=false;controls();}
  }
  $('history-sync').addEventListener('click',sync);
  $('history-days').addEventListener('change',()=>{days=Number($('history-days').value);page=1;load();});
  for(const value of ['video','creator'])$('history-'+value+'s-tab').addEventListener('click',()=>{kind=value;page=1;load();});
  $('history-previous').addEventListener('click',()=>{if(page>1){page--;load();}});
  $('history-next').addEventListener('click',()=>{if(page*result.limit<result.total){page++;load();}});
  return {show(){page=1;return load();}};
}
