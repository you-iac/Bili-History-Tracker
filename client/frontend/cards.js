// cards.js 共用 UP、视频和投稿卡片；密度决定信息层级，管理只改变本地关注。见 docs/client-catalog.md。
import {compactNumber,signedNumber,detailHref,publishedAt} from './view.mjs';

const statusNames={ok:'观测正常',running:'正在采集',pending:'等待采集',recovered:'已恢复',stale:'数据较旧',paused:'已暂停',unavailable:'暂不可用',not_recorded:'尚未记录'};
export function cardStatus(item) {return item?statusNames[item.status]||(item.metadata_source?'当前资料已获取':item.tracking?'服务器跟踪中':'未在公共跟踪'):'待获取资料';}
export function renderCards({container,data,kind,publicMode,button,images,onAdd,onRefresh,onRetry,onRemove,onScan,resolveImages=true}) {
  const node=(tag,text,className)=>{const el=document.createElement(tag);if(text!=null)el.textContent=text;if(className)el.className=className;return el;};
  const fragment=document.createDocumentFragment();
  for(const follow of data.items) {
    const item=publicMode?follow:(follow.current?.item||follow.summary||follow.data?.item),followed=publicMode?follow.followed:true,creator=kind==='creator';
    const external=kind==='video'&&item?.recorded===false;
    const href=external?'https://www.bilibili.com/video/'+follow.id:detailHref(kind,follow.id,publicMode?'public':'');
    const card=node('article',null,'entity-card '+kind+'-card'),top=node('div',null,'entity-top'+(creator?'':' video-top'));
    const avatar=node('span',creator?(item?.name||follow.id).slice(0,1):'▷','entity-avatar'+(creator?'':' video-placeholder'));
    images.show(avatar,{kind,id:follow.id,seq:item?.seq||0,url:item?.cover||'',resolve:resolveImages});top.append(avatar);
    const title=node('a',item?.name||follow.id,'entity-title');title.href=href;
    if(external){title.target='_blank';title.rel='noopener noreferrer';}
    const identity=node('p',(creator?'UID ':'')+follow.id+(item?.owner_name?' · '+item.owner_name:''),'entity-id');
    const basic=node('p',compactNumber(item?.metric)+(creator?' 粉丝':' 播放'),'entity-basic');
    const stats=node('div',null,'entity-numbers');
    for(const [value,label,signed] of [[item?.metric,creator?'粉丝':'播放',false],[item?.growth,'近7日变化',true]]) {
      const cell=node('div'),strong=node('strong',signed?signedNumber(value):compactNumber(value),signed?(value>0?'positive':value<0?'negative':''):'');
      strong.title=Number.isFinite(value)?value.toLocaleString('zh-CN'):'暂无数据';cell.append(strong,node('span',label));stats.append(cell);
    }
    const extra=node('div',null,'entity-extra');
    if(creator){extra.append(node('span','近7日投稿 '+compactNumber(item?.recent_videos)),node('small','本地已关注视频范围'));}
    extra.append(node('span',(creator?'最近投稿 ':'发布于 ')+publishedAt(item?.published_at)));
    if(creator)extra.append(node('span','本地已关注视频播放合计 '+compactNumber(item?.video_views)));
    if(item?.metadata_source==='server'||item?.metadata_source==='bilibili+server')extra.append(node('small','当前资料含服务器已有记录兜底'));
    if(item?.history_error)extra.append(node('small',item.history_error));
    if(item?.metadata_error)extra.append(node('small',item.metadata_error));
    const actions=node('div',null,'entity-actions'),details=node('a',external?'在 B站打开 ↗':'查看详情 →','text-button');details.href=href;
    if(external){details.target='_blank';details.rel='noopener noreferrer';}actions.append(details);
    if(!followed)actions.append(button('关注',()=>onAdd(kind,follow.id),'button primary'));
    else {
      const more=node('details',null,'card-more'),summary=node('summary','管理');more.append(summary);const menu=node('div',null,'card-menu');
      if(onRefresh)menu.append(button('刷新资料',()=>onRefresh(follow)));
      if(onRetry&&follow.pending_submission)menu.append(button('重试提交',()=>onRetry(follow)));
      if(creator&&onScan)menu.append(button('获取全部视频',()=>onScan(follow.id)));
      if(onRemove)menu.append(button('取消关注',()=>onRemove(follow),'text-button danger'));
      more.append(menu);actions.append(more);
    }
    card.append(top,title,identity,basic,stats,extra);
    if(!publicMode&&follow.current?.available){const current=follow.current;card.append(node('small',`${current.source==='platform'?'平台资料':'服务器最后记录'} · ${current.item.observed_at?new Date(current.item.observed_at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'观测时间未知'}`,'muted'));}
    if(!publicMode&&follow.last_error)card.append(node('p',follow.last_error,'follow-error'));
    card.append(actions);
    card.addEventListener('click',event=>{if(!event.target.closest('a,button,summary,details,input,select')&&!getSelection()?.toString())title.click();});
    fragment.append(card);
  }
  if(!data.items.length)fragment.append(node('p',publicMode?'暂无匹配的未关注视频或公开记录。':'暂无匹配的本地关注。','empty-copy'));
  container.replaceChildren(fragment);
}
