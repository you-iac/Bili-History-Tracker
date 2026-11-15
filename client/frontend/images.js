// images.js 仅从受保护的Agent接口解析图片地址；可见区域懒加载、最多4个元数据请求。
// 浏览器直连白名单CDN，不发送本地会话或来源信息；失败保留占位。见 docs/client-launch-media.md。
export function safeImageURL(raw) {
  if(typeof raw!=='string')return '';
  raw=raw.trim();if(raw.startsWith('//'))raw=`https:${raw}`;
  if(raw.length>4096||/[\\\r\n\t]/.test(raw))return '';
  try{const url=new URL(raw),host=url.hostname.toLowerCase();
    if(!['https:','http:'].includes(url.protocol)||url.username||url.password||url.port||/^https?:\/\/[^/]*:/i.test(raw))return '';
    if(!['hdslb.com','biliimg.com'].some(domain=>host===domain||host.endsWith(`.${domain}`)))return '';
    url.protocol='https:';url.hash='';return url.href;
  }catch{return '';}
}
export function snapshotImageURL(kind,snapshot) {return safeImageURL(kind==='creator'?snapshot?.card?.face:kind==='video'?snapshot?.pic:'');}

export function initImages({api}) {
  let source=null,generation=0,active=0,queue=[],cleanupScheduled=false;
  const records=new WeakMap(),watching=new Set(),cache=new Map(),failures=new Map(),broken=new Map();
  const observer=typeof IntersectionObserver==='function'?new IntersectionObserver(entries=>{for(const entry of entries)if(entry.isIntersecting){observer.unobserve(entry.target);watching.delete(entry.target);const record=records.get(entry.target);if(record)enqueue(record);}},{rootMargin:'150px'}):null;
  function valid(record){return record.generation===generation&&records.get(record.element)===record&&record.element.isConnected;}
  function assign(record,raw){if(!valid(record))return;const url=safeImageURL(raw);if(!url||(broken.get(url)||0)>Date.now()){record.element.dataset.mediaState='unavailable';return;}
    record.element.querySelector('img')?.remove();const image=document.createElement('img');image.alt='';image.loading='lazy';image.decoding='async';image.referrerPolicy='no-referrer';
    image.addEventListener('load',()=>{if(valid(record))record.element.dataset.mediaState='loaded';},{once:true});
    image.addEventListener('error',()=>{image.remove();if(valid(record)){if(broken.size>=500)broken.clear();broken.set(url,Date.now()+60000);record.element.dataset.mediaState='unavailable';record.element.title='图片暂不可用';}},{once:true});
    record.element.dataset.mediaState='loading';image.src=url;record.element.append(image);
  }
  function key(record){return `${record.kind}:${record.id}:${record.seq}`;}
  async function resolve(record){const id=key(record),cached=cache.get(id);if(cached&&(record.seq!==0||Date.now()-cached.at<60000))return cached.promise.catch(()=>'');
    if((failures.get(id)||0)>Date.now())return '';
    const current=generation,promise=api(`/api/images/${record.kind}/${encodeURIComponent(record.id)}?seq=${record.seq}`).then(data=>safeImageURL(data.url));cache.set(id,{promise,at:Date.now()});
    while(cache.size>500)cache.delete(cache.keys().next().value);
    try{return await promise;}catch{if(current===generation){cache.delete(id);if(failures.size>500)failures.clear();failures.set(id,Date.now()+30000);}return '';}
  }
  function drain(){while(active<4&&queue.length){const record=queue.shift();if(!valid(record))continue;active++;resolve(record).then(url=>assign(record,url)).catch(()=>{if(valid(record))record.element.dataset.mediaState='unavailable';}).finally(()=>{active--;drain();});}}
  function enqueue(record){if(record.queued)return;record.queued=true;queue.push(record);drain();}
  function prune(){cleanupScheduled=false;for(const element of watching)if(!element.isConnected){observer?.unobserve(element);watching.delete(element);}}
  return {
    setSource(value){if(source===value)return;source=value;generation++;cache.clear();failures.clear();broken.clear();queue=[];for(const element of watching)observer?.unobserve(element);watching.clear();},
    show(element,{kind,id,seq=0,url=''}){
      observer?.unobserve(element);watching.delete(element);element.querySelector('img')?.remove();element.dataset.mediaState='pending';element.classList.add('remote-image');
      const record={element,kind,id,seq:Number.isSafeInteger(seq)&&seq>=0?seq:0,generation,queued:false};records.set(element,record);
      const known=safeImageURL(url);
      if(known){queueMicrotask(()=>assign(record,known));}else if(observer){watching.add(element);observer.observe(element);}else{queueMicrotask(()=>enqueue(record));}
      if(!cleanupScheduled){cleanupScheduled=true;queueMicrotask(prune);}
    }
  };
}
