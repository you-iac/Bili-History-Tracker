// cards.js 展示私人关注与公开记录，图形仅作占位；不发起逐项远程查询。见 docs/client-catalog.md。
import {compactNumber,signedNumber,detailHref,followState} from './view.mjs';

const statusNames={ok:'观测正常',running:'正在采集',pending:'等待采集',recovered:'已恢复',stale:'数据较旧',paused:'已暂停',unavailable:'暂不可用',not_recorded:'尚未记录'};
export function cardStatus(item) {return item?statusNames[item.status]||(item.tracking?'服务器跟踪中':'未在公共跟踪'):'待获取资料';}
export function renderCards({container,data,kind,publicMode,button,onAdd,onRefresh,onRetry,onRemove,onScan}) {
  const node=(tag,text,className)=>{const el=document.createElement(tag);if(text!=null)el.textContent=text;if(className)el.className=className;return el;};
  const fragment=document.createDocumentFragment();
  for(const follow of data.items) {
    const item=publicMode?follow:follow.data?.item,followed=publicMode?follow.followed:true;
    const href=detailHref(kind,follow.id,publicMode?'public':''),card=node('article',null,`entity-card ${kind}-card`);
    const top=node('div',null,'entity-top'),avatar=node('span',kind==='creator'?(item?.name||follow.id).slice(0,1):'▷',`entity-avatar ${kind==='video'?'video-placeholder':''}`);
    const status=node('span',cardStatus(item),'record-status');status.dataset.state=item?.status||'unknown';top.append(avatar,status);
    const title=node('a',item?.name||follow.id,'entity-title');title.href=href;
    const identity=node('p',`${kind==='creator'?'UID ':''}${follow.id}`,'entity-id');
    if(kind==='video'&&item?.owner_name)identity.append(node('span',` · ${item.owner_name}`));
    const stats=node('div',null,'entity-numbers');
    for(const [value,label] of [[item?.metric,kind==='creator'?'粉丝':'播放'],[item?.seq>0?item.seq:null,'内容版本']]){const cell=node('div'),strong=node('strong',compactNumber(value));strong.title=Number.isFinite(value)?value.toLocaleString('zh-CN'):'尚无数据';cell.append(strong,node('span',label));stats.append(cell);}
    const foot=node('div',null,'entity-foot');
    const date=item?.observed_at?new Date(item.observed_at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'暂无成功观测';
    foot.append(node('span',date),node('span',`近7日 ${signedNumber(item?.growth)}`,`growth ${item?.growth>0?'positive':item?.growth<0?'negative':''}`));
    const personal=node('div',null,'entity-personal');personal.append(node('span',publicMode?(followed?'已本地关注':'尚未本地关注'):followState(follow),`local-state${!publicMode&&follow.pending_submission?' pending':''}`));
    if(!publicMode&&follow.last_error){const error=node('p',follow.last_error,'follow-error');personal.append(error);}
    const actions=node('div',null,'entity-actions'),details=node('a','查看详情 →','text-button');details.href=href;actions.append(details);
    if(!followed)actions.append(button('关注并跟踪',()=>onAdd(kind,follow.id),'button primary'));
    if(!publicMode){const more=document.createElement('details');more.className='card-more';const summary=node('summary','管理');more.append(summary);const menu=node('div',null,'card-menu');menu.append(button('刷新资料',()=>onRefresh(follow)));if(follow.pending_submission||!follow.remote?.tracking)menu.append(button('重试提交',()=>onRetry(follow)));if(kind==='creator')menu.append(button('获取全部视频',()=>onScan(follow.id)));menu.append(button('取消本地关注',()=>onRemove(follow),'text-button danger'));more.append(menu);actions.append(more);}
    card.append(top,title,identity,stats,foot,personal,actions);
    card.addEventListener('click',event=>{if(!event.target.closest('a,button,summary,details,input,select')&&!getSelection()?.toString())location.hash=href;});
    fragment.append(card);
  }
  if(!data.items.length){const empty=node('div',publicMode?'服务器暂无匹配记录。可按 UID/BV 添加关注。':'暂无匹配的本地关注。可以切到服务器公开库搜索，或按 UID/BV 添加。','empty-copy');fragment.append(empty);}
  container.replaceChildren(fragment);
}
