// view.test.mjs 验证未知总量、部分结果和北京时间边界，避免界面误报完成。
import test from 'node:test';
import assert from 'node:assert/strict';
import {progress, publishedAt, rangeLabel, stateLabel, viewForHash, followState} from './view.mjs';

test('侧栏路由可直接刷新，启动密钥和未知片段不成为页面', () => {
  for (const view of ['overview','creators','videos','settings','discovery']) assert.equal(viewForHash(`#${view}`),view);
  assert.equal(viewForHash('#token=private'),'overview');
  assert.equal(viewForHash('#unknown'),'overview');
});

test('本地关注保存和服务器跟踪分别确认，失败明确进入重试', () => {
  assert.equal(followState({pending_submission:true,last_error:'offline',remote:{tracking:true}}),'待提交 · 后台将重试');
  assert.equal(followState({pending_submission:true}),'正在申请服务器跟踪');
  assert.equal(followState({remote:{tracking:true}}),'服务器跟踪中');
  assert.equal(followState({remote:{tracking:false,recorded:true}}),'服务器已记录，未跟踪');
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
