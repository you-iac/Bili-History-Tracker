// trend.mjs 绘制真实UTC日采样；缺失日期断线，不插值，不把未知当作0。见 docs/client-catalog.md。
import {compactNumber, signedNumber, utcDay} from './view.mjs';

const DAY=86400000;
export function trendModel(input) {
  const daily=new Map();
  for(const p of (input||[]).filter(p=>Number.isFinite(p.at)&&p.at>0&&Number.isFinite(p.value)).sort((a,b)=>a.at-b.at)) daily.set(Math.floor(p.at/DAY),{at:p.at,value:p.value});
  const points=[...daily.values()],segments=[];
  for(const p of points){const last=segments.at(-1)?.at(-1);if(!last||Math.floor(p.at/DAY)-Math.floor(last.at/DAY)>1)segments.push([]);segments.at(-1).push(p);}
  const values=points.map(p=>p.value),low=points.length?Math.min(...values):0,high=points.length?Math.max(...values):0;
  const padding=Math.max((high-low)*.15,Math.abs(high)*.001,1);
  return {points,segments,min:low-padding,max:high+padding,change:points.length>1?points.at(-1).value-points[0].value:null};
}

export function renderTrend(container,input,label='指标变化') {
  container.replaceChildren();
  const model=trendModel(input),{points,segments,min,max}=model;
  if(!points.length){const empty=document.createElement('p');empty.className='trend-empty';empty.textContent='暂无真实日采样，开始采集后会逐日显示。';container.append(empty);return model;}
  const NS='http://www.w3.org/2000/svg';
  const el=(tag,attrs={},text)=>{const node=document.createElementNS(NS,tag);for(const [k,v] of Object.entries(attrs))node.setAttribute(k,v);if(text!=null)node.textContent=text;return node;};
  const width=Math.max(360,container.clientWidth||1000),height=280,left=64,right=24,top=24,bottom=40,plotW=width-left-right,plotH=height-top-bottom;
  const first=points[0].at,last=points.at(-1).at,span=last-first;
  const x=p=>span?left+(p.at-first)/span*plotW:left+plotW/2,y=p=>top+(max-p.value)/(max-min)*plotH;
  const svg=el('svg',{viewBox:`0 0 ${width} ${height}`,role:'img','aria-label':`${label}，${points.length}个真实日采样，净变化${signedNumber(model.change)}`});
  svg.append(el('title',{},`${label}，缺失日期留空。详细值见下方采样表。`));
  for(let i=0;i<=4;i++){const yy=top+i*plotH/4,value=max-i*(max-min)/4;svg.append(el('line',{x1:left,y1:yy,x2:width-right,y2:yy,class:'trend-grid'}),el('text',{x:left-12,y:yy+4,'text-anchor':'end',class:'trend-label'},compactNumber(Number(value.toPrecision(4)))));}
  const ticks=points.length===1?[points[0]]:[points[0],points[Math.floor((points.length-1)/2)],points.at(-1)];
  for(const p of [...new Set(ticks)])svg.append(el('text',{x:x(p),y:height-12,'text-anchor':'middle',class:'trend-label'},utcDay(p.at).slice(5)));
  for(const segment of segments){if(segment.length>1){const coords=segment.map(p=>`${x(p)},${y(p)}`).join(' ');svg.append(el('polygon',{points:`${x(segment[0])},${top+plotH} ${coords} ${x(segment.at(-1))},${top+plotH}`,class:'trend-area'}),el('polyline',{points:coords,class:'trend-line'}));}if(segment.length===1||points.length<=100)for(const p of segment)svg.append(el('circle',{cx:x(p),cy:y(p),r:3,class:'trend-dot'}));}
  const guide=el('line',{y1:top,y2:top+plotH,class:'trend-guide',visibility:'hidden'}),dot=el('circle',{r:5,class:'trend-dot',visibility:'hidden'});svg.append(guide,dot);
  const tooltip=document.createElement('div');tooltip.className='trend-tooltip';tooltip.hidden=true;
  svg.addEventListener('pointermove',event=>{const rect=svg.getBoundingClientRect(),position=(event.clientX-rect.left)/rect.width*width;let nearest=points[0];for(const p of points)if(Math.abs(x(p)-position)<Math.abs(x(nearest)-position))nearest=p;guide.setAttribute('x1',x(nearest));guide.setAttribute('x2',x(nearest));guide.setAttribute('visibility','visible');dot.setAttribute('cx',x(nearest));dot.setAttribute('cy',y(nearest));dot.setAttribute('visibility','visible');tooltip.textContent=`${utcDay(nearest.at)} UTC · ${nearest.value.toLocaleString('zh-CN')}`;tooltip.hidden=false;tooltip.style.left=`${Math.min(75,Math.max(5,x(nearest)/width*100))}%`;});
  svg.addEventListener('pointerleave',()=>{tooltip.hidden=true;guide.setAttribute('visibility','hidden');dot.setAttribute('visibility','hidden');});
  container.append(svg,tooltip);return model;
}
