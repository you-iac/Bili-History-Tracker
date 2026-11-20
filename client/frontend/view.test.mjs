// view.test.mjs 验证未知总量、部分结果和北京时间边界，避免界面误报完成。
import test from 'node:test';
import assert from 'node:assert/strict';
import {progress, publishedAt, rangeLabel, stateLabel, viewForHash, followState, routeForHash, detailHref, compactNumber, snapshotChanges, detailReadMessage} from './view.mjs';

test('发布时间兼容服务器毫秒与投稿扫描秒，缺失仍为未知',()=>{
  const seconds=1794787200;
  assert.equal(publishedAt(seconds),publishedAt(seconds*1000));
  assert.equal(publishedAt(null),'—');
});

test('侧栏路由可直接刷新，启动密钥和未知片段不成为页面', () => {
  for (const view of ['overview','creators','videos','settings','discovery']) assert.equal(viewForHash(`#${view}`),view);
  assert.equal(viewForHash('#token=private'),'overview');
  assert.equal(viewForHash('#unknown'),'overview');
});

test('本地关注保存和服务器跟踪分别确认，失败明确进入重试', () => {
  assert.equal(followState({pending_submission:true,last_error:'offline',remote:{tracking:true}}),'最近确认已跟踪 · 本次核对未完成');
  assert.equal(followState({pending_submission:true}),'正在申请服务器跟踪');
  assert.equal(followState({remote:{tracking:true}}),'服务器跟踪中');
  assert.equal(followState({remote:{tracking:false,recorded:true}}),'服务器已记录，未跟踪');
  assert.equal(followState({pending_submission:true,last_stage:'confirm',last_outcome:'unknown'}),'处理结果待确认 · 后台将先核对');
});

test('缓存提示按业务码区分无资料、限流和真正连接失败',()=>{
  assert.equal(detailReadMessage({cached:true,read_code:'DETAIL_NOT_RECORDED',read_error:'缺少资料',target:{tracking:true}}),'服务器已跟踪，暂无成功观测资料。 当前显示本地缓存。');
  assert.equal(detailReadMessage({cached:true,read_code:'RATE_LIMITED',read_error:'限流'}),'服务器请求较多，请稍后重试。 当前显示本地缓存。');
  assert.equal(detailReadMessage({cached:true,read_code:'CONNECTION_FAILED',read_error:'连接失败'}),'无法连接服务器。 当前显示本地缓存。');
  assert.equal(detailReadMessage({read_code:'DETAIL_NOT_RECORDED',read_error:'缺少资料'}),'服务器暂无成功观测资料。');
  assert.equal(followState({pending_submission:true,needs_attention:true}),'需要手动核对');
  assert.equal(followState({pending_submission:true,remote:{tracking_state:'paused'}}),'服务器已暂停');
});

test('详情深链接保留目标和来源，非法标识不会被作为远程路径',()=>{
  assert.deepEqual(routeForHash('#creators/593014440?scope=public'),{view:'detail',kind:'creator',id:'593014440',scope:'public',section:'creators',hash:'#creators/593014440?scope=public'});
  assert.equal(detailHref('video','BV17x411w7KC'),'#videos/BV17x411w7KC');
  assert.equal(viewForHash('#videos/BV17x411w7KC'),'detail');
  assert.equal(routeForHash('#videos?scope=public').scope,'public');
  for(const invalid of ['#creators/../../settings','#creators/%31','#videos/javascript:alert(1)','#creators/123/extra','#settings/123'])assert.equal(viewForHash(invalid),'overview');
});

test('卡片紧凑数字保留零和负数，未知不伪造成零',()=>{
  assert.equal(compactNumber(null),'—');assert.equal(compactNumber(0),'0');assert.equal(compactNumber(228459),'22.8万');assert.equal(compactNumber(-136),'-136');
});

test('版本对比展示内容变化，不把计数变化列为改名或改简介',()=>{
  assert.deepEqual(snapshotChanges('creator',{card:{name:'UP',sign:'旧'},follower:100},{card:{name:'UP',sign:'新'},follower:101}),[{key:'sign',label:'签名',before:'旧',after:'新'}]);
  assert.deepEqual(snapshotChanges('video',{title:'同名',stat:{view:1}},{title:'同名',stat:{view:2}}),[]);
  assert.equal(snapshotChanges('video',{title:'之前'},{title:'<script>之后</script>'})[0].after,'<script>之后</script>');
});

test('只有明确完成时显示 100%，未知总量不伪造进度', () => {
  assert.equal(progress(130,120,'partial'),99);
  assert.equal(progress(0,0,'running'),0);
  assert.equal(progress(0,null,'running'),0);
  assert.equal(progress(0,0,'complete'),100);
  assert.equal(progress(60,150,'running'),40);
});
test('中断、部分和取消状态与完成区分', () => {
  assert.equal(stateLabel('interrupted'),'待继续');
  assert.equal(stateLabel('partial'),'部分结果');
  assert.notEqual(stateLabel('cancelled'),stateLabel('complete'));
});
test('缺失日期保留未知，投稿日期使用北京时间', () => {
  assert.equal(publishedAt(0),'—');
  assert.equal(publishedAt(null),'—');
  assert.equal(publishedAt(Date.parse('2026-11-11T17:00:00Z')/1000),'2026/11/12');
});
test('跨页范围使用真实结果数量', () => {
  assert.equal(rangeLabel(100,5,105),'101–105 / 共 105 条');
  assert.equal(rangeLabel(0,0,0),'共 0 条视频 ID');
});
