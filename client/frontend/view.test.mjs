// view.test.mjs 验证未知总量、部分结果和北京时间边界，避免界面误报完成。
import test from 'node:test';
import assert from 'node:assert/strict';
import {progress, publishedAt, rangeLabel, stateLabel} from './view.mjs';

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
