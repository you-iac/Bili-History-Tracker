// history.test.mjs 用最小 DOM 验证历史页的会话门禁、自动同步、并发读取和显式关注。
// 不模拟网络副作用，api 桩分别控制缓存与平台请求的完成顺序。
import test from 'node:test';
import assert from 'node:assert/strict';
import {initHistory} from './history.js';

class Element {
  constructor(tag='div') {this.tagName=tag;this.children=[];this.dataset={};this.attributes={};this.listeners={};this.isConnected=true;this.classList={add(){},toggle(){}};}
  append(...items){for(const item of items){if(item.tagName==='fragment')this.append(...item.children);else{this.children.push(item);item.parent=this;}}}
  replaceChildren(...items){this.children=[];this.append(...items);}
  setAttribute(key,value){this.attributes[key]=value;}
  addEventListener(name,fn){this.listeners[name]=fn;}
  async click(){if(!this.disabled)return this.listeners.click?.({target:this});}
  closest(selector){return selector.split(',').includes(this.tagName)?this:this.parent?.closest(selector);}
  querySelectorAll(selector){return this.children.flatMap(el=>{
    const matches=selector==='[data-history-follow]'?Boolean(el.dataset.historyFollow):el.tagName===selector;
    return [...(matches?[el]:[]),...el.querySelectorAll(selector)];
  });}
  querySelector(selector){return this.querySelectorAll(selector)[0];}
  remove(){if(this.parent)this.parent.children=this.parent.children.filter(el=>el!==this);}
}
function harness(t,handler=()=>undefined,savedSize='standard'){
  const elements=new Map(),calls=[],notices=[],events=new Map(),observed=new Map(),storage=new Map([['bht.card-size',savedSize]]);
  const originalDocument=globalThis.document,originalWindow=globalThis.window,originalStorage=globalThis.localStorage,originalObserver=globalThis.IntersectionObserver;
  globalThis.document={getElementById(id){if(!elements.has(id))elements.set(id,new Element());return elements.get(id);},createElement:tag=>new Element(tag),createDocumentFragment:()=>new Element('fragment')};
  globalThis.window={getSelection:()=>null,addEventListener(type,listener){events.set(type,listener);},dispatchEvent(event){events.get(event.type)?.(event);}};
  globalThis.localStorage={getItem:key=>storage.get(key),setItem:(key,value)=>storage.set(key,value)};
  globalThis.IntersectionObserver=class {
    constructor(callback){this.callback=callback;}
    observe(element){observed.set(element,this);}
    unobserve(element){observed.delete(element);}
  };
  t.after(()=>{globalThis.document=originalDocument;globalThis.window=originalWindow;globalThis.localStorage=originalStorage;globalThis.IntersectionObserver=originalObserver;});
  const api=async(path,options)=>{calls.push({path,options});const value=await handler(path,options);return value??(path==='/api/history/sync'?{added:1,updated:0,synced_at:'2026-11-21T14:00:00Z'}:{items:[],page:1,limit:20,total:0});};
  const ui=initHistory({api,notify:(...args)=>notices.push(args)});
  return {ui,calls,notices,storage,$:id=>document.getElementById(id),intersect(element){observed.get(element)?.callback([{target:element,isIntersecting:true}]);}};
}
const flush=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};};
const video={bvid:'BV17x411w7KC',title:'视频标题',author_id:'123',author_name:'作者',viewed_at:1795269600,cover:'http://i0.hdslb.com/cover.jpg'};
const page=items=>({items,page:1,limit:20,total:items.length});

test('首次打开等待会话，自动读缓存和同步；返回与筛选不重复同步',async t=>{
  const h=harness(t);await h.ui.show();assert.equal(h.calls.length,0);
  h.ui.setConnected(true,true);await flush();
  assert.equal(h.calls.filter(x=>x.path==='/api/history/sync').length,1);
  assert.equal(h.calls.find(x=>x.path==='/api/history/sync').options.method,'POST');
  h.ui.leave();await h.ui.show();await h.$('history-creators-tab').click();await flush();
  assert.equal(h.$('history-creators-tab').attributes['aria-pressed'],'true');
  assert.equal(h.$('history-videos-tab').attributes['aria-pressed'],'false');
  h.$('history-days').value='3';h.$('history-days').listeners.change();await flush();
  assert.match(h.calls.at(-1).path,/creators\?days=3/);
  assert.equal(h.calls.filter(x=>x.path==='/api/history/sync').length,1);
});

test('没有 Cookie 也能读缓存，配置 Cookie 后自动同步',async t=>{
  const h=harness(t,path=>path.includes('/videos?')?page([video]):undefined);
  h.ui.setConnected(true,false);await h.ui.show();
  assert.equal(h.$('history-rows').children.length,1);assert.equal(h.$('history-settings').hidden,false);
  assert.equal(h.calls.some(x=>x.path==='/api/history/sync'),false);
  h.ui.setConnected(true,true);await flush();assert.equal(h.calls.filter(x=>x.path==='/api/history/sync').length,1);
});

test('同步失败保留本地列表与错误，允许手动重试，离开后不追加读取',async t=>{
  const pending=deferred();let attempt=0;
  const h=harness(t,path=>path==='/api/history/sync'?(++attempt===1?Promise.reject(Error('Cookie 已失效')):pending.promise):page([video]));
  h.ui.setConnected(true,true);await h.ui.show();
  assert.equal(h.$('history-rows').children.length,1);assert.match(h.$('history-state').textContent,/Cookie 已失效/);
  const retry=h.$('history-sync').click();assert.equal(h.$('history-sync').disabled,true);
  const count=h.calls.length;h.ui.leave();pending.resolve({added:0,updated:0,synced_at:'2026-11-21T14:00:00Z'});await retry;
  assert.equal(h.calls.length,count);assert.equal(h.$('history-sync').disabled,false);
});

test('快速切换标签时，较晚返回的旧视频请求不能覆盖 UP 列表',async t=>{
  const old=deferred(),h=harness(t,path=>path.includes('/videos?')?old.promise:page([{id:'123',name:'作者',videos:2,last_viewed_at:1795269600}]));
  h.ui.setConnected(true,false);const first=h.ui.show();await h.$('history-creators-tab').click();await flush();
  old.resolve(page([video]));await first;
  assert.equal(h.$('history-rows').dataset.kind,'creator');assert.match(h.$('history-page').textContent,/UP 主/);
});

test('卡片与作者进入详情，原站链接独立；关注不跳转、不重复提交并显示待提交',async t=>{
  const pending=deferred(),h=harness(t,path=>path==='/api/follows'?pending.promise:page([video]));
  h.ui.setConnected(true,false);await h.ui.show();await flush();
  const card=h.$('history-rows').children[0],anchors=card.querySelectorAll('a');
  assert.equal(anchors[0].href,'#videos/BV17x411w7KC');assert.equal(anchors[2].href,'#creators/123');
  assert.equal(anchors.at(-1).target,'_blank');
  assert.equal(card.querySelector('img').src,'https://i0.hdslb.com/cover.jpg');
  const button=card.querySelector('button'),follow=button.click();await button.click();
  assert.equal(button.textContent,'关注中…');assert.equal(h.calls.filter(x=>x.path==='/api/follows').length,1);
  pending.resolve({pending_submission:true});await follow;
  assert.equal(button.textContent,'已关注 · 待提交');assert.equal(button.disabled,true);assert.match(h.notices.at(-1)[0],/后台将重试/);
});

test('已有关注不可重复关注；重新进入以后使用最新关注状态',async t=>{
  let followed=true;const h=harness(t,()=>page([{...video,followed}]));
  h.ui.setConnected(true,false);await h.ui.show();
  assert.equal(h.$('history-rows').querySelector('button').textContent,'已关注');
  followed=false;h.ui.leave();await h.ui.show();
  assert.equal(h.$('history-rows').querySelector('button').disabled,false);
});

test('读取失败保留内容并提供重试，重试成功恢复分页',async t=>{
  let fail=false;const h=harness(t,()=>fail?Promise.reject(Error('读取失败')):page([video]));
  h.ui.setConnected(true,false);await h.ui.show();fail=true;await h.ui.show();
  assert.equal(h.$('history-rows').children.length,1);assert.equal(h.$('history-retry').hidden,false);
  fail=false;await h.$('history-retry').click();assert.equal(h.$('history-retry').hidden,true);
});

test('继承库的密度设置，三档分别请求30/20/12项并重置页码',async t=>{
  const h=harness(t,path=>{
    const query=new URL(path,'http://local').searchParams,limit=Number(query.get('limit')),page=Number(query.get('page'));
    return {items:[video],total:80,limit,page};
  },'compact');
  h.ui.setConnected(true,false);await h.ui.show();
  assert.equal(h.$('history-card-size').value,'compact');assert.equal(h.$('history-rows').dataset.size,'compact');
  assert.match(h.calls.at(-1).path,/limit=30/);
  await h.$('history-next').click();await flush();assert.match(h.calls.at(-1).path,/page=2/);
  for(const [size,limit] of [['large',12],['standard',20],['compact',30]]){
    h.$('history-card-size').value=size;h.$('history-card-size').listeners.change();await flush();
    assert.equal(h.$('history-rows').dataset.size,size);assert.equal(h.storage.get('bht.card-size'),size);
    assert.match(h.calls.at(-1).path,new RegExp(`page=1&limit=${limit}`));assert.match(h.$('history-page').textContent,new RegExp(`每页 ${limit}`));
  }
  window.dispatchEvent(new CustomEvent('bht-density',{detail:'large'}));await flush();
  assert.equal(h.$('history-card-size').value,'large');assert.match(h.calls.at(-1).path,/limit=12/);
});

test('未关注筛选保留范围、类型与密度，关注成功后重新取页和统计',async t=>{
  let followed=false;
  const h=harness(t,path=>{
    if(path==='/api/follows'){followed=true;return {pending_submission:true};}
    const query=new URL(path,'http://local').searchParams;
    return {items:query.has('unfollowed')&&followed?[]:[video],total:query.has('unfollowed')&&followed?0:1,page:1,limit:Number(query.get('limit'))};
  },'large');
  h.ui.setConnected(true,false);await h.ui.show();
  h.$('history-days').value='7';h.$('history-days').listeners.change();await flush();
  await h.$('history-unfollowed').click();await flush();
  assert.equal(h.$('history-unfollowed').attributes['aria-pressed'],'true');
  assert.match(h.calls.at(-1).path,/videos\?days=7&page=1&limit=12&unfollowed=1/);
  await h.$('history-rows').querySelector('button').click();
  assert.match(h.$('history-page').textContent,/共 0 个视频（未关注）/);
  assert.equal(h.$('history-rows').querySelectorAll('button').length,0);
  assert.match(h.$('history-rows').querySelector('strong').textContent,/未关注/);
  await h.$('history-creators-tab').click();await flush();assert.match(h.calls.at(-1).path,/creators\?days=7&page=1&limit=12&unfollowed=1/);
  await h.$('history-unfollowed').click();await flush();assert.doesNotMatch(h.calls.at(-1).path,/unfollowed/);
  assert.equal(h.$('history-unfollowed').attributes['aria-pressed'],'false');
});

test('未关注列表中关注失败保留卡片，筛选和大小切换不触发同步',async t=>{
  const h=harness(t,path=>path==='/api/follows'?Promise.reject(Error('关注失败')):page([video]));
  h.ui.setConnected(true,false);await h.ui.show();await h.$('history-unfollowed').click();await flush();
  const button=h.$('history-rows').querySelector('button');await button.click();
  assert.equal(button.disabled,false);assert.equal(h.$('history-rows').children.length,1);
  assert.match(h.notices.at(-1)[0],/关注失败/);assert.equal(h.calls.some(x=>x.path==='/api/history/sync'),false);
});

test('历史 UP 无缓存时可见才补取头像，最多4个请求；已有地址直显，失败保留占位',async t=>{
  const requests=new Map(),items=Array.from({length:6},(_,i)=>({id:String(123+i),name:'作者'+i,videos:1,last_viewed_at:1795269600,...(i===5?{cover:'http://i0.hdslb.com/cached.jpg'}:{})}));
  const h=harness(t,path=>{
    if(path.includes('/history/creators?'))return page(items);
    if(path.startsWith('/api/images/')){const pending=deferred();requests.set(path,pending);return pending.promise;}
  });
  h.ui.setConnected(true,false);await h.ui.show();await h.$('history-creators-tab').click();await flush();
  const avatars=h.$('history-rows').children.map(card=>card.children[0].children[0]);
  assert.equal(requests.size,0);assert.equal(avatars[5].querySelector('img').src,'https://i0.hdslb.com/cached.jpg');
  for(const avatar of avatars.slice(0,5))h.intersect(avatar);
  assert.equal(requests.size,4);
  requests.get('/api/images/creator/123?seq=0').resolve({url:'http://i0.hdslb.com/live.jpg'});await flush();
  assert.equal(requests.size,5);assert.equal(avatars[0].querySelector('img').src,'https://i0.hdslb.com/live.jpg');
  for(const [path,pending] of requests){
    if(path.includes('/126?'))pending.reject(Error('暂时不可用'));
    else pending.resolve({url:''});
  }
  await flush();assert.equal(avatars[3].querySelector('img'),undefined);assert.equal(avatars[3].textContent,'作');
  assert.equal(avatars[3].dataset.mediaState,'unavailable');assert.equal(avatars[4].dataset.mediaState,'unavailable');
  assert.equal(h.calls.filter(x=>x.path==='/api/follows').length,0);
});
