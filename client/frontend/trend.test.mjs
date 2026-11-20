// trend.test.mjs 验证UTC采样去重、缺口与净变化，避免趋势图补造数据。
import test from 'node:test';
import assert from 'node:assert/strict';
import {trendModel} from './trend.mjs';
const t=Date.parse('2026-11-01T00:00:00Z'),day=86400000;

test('真实日采样之间缺两天时断开连线，净变化保留负值',()=>{
  const model=trendModel([{at:t,value:100},{at:t+day,value:99},{at:t+4*day,value:95}]);
  assert.equal(model.points.length,3);assert.deepEqual(model.segments.map(x=>x.length),[2,1]);assert.equal(model.change,-5);
});
test('UTC同日保留最新值，未知和无效日期不成为0',()=>{
  const model=trendModel([{at:t+day,value:0},{at:t+3600000,value:5},{at:t,value:9},{at:t+2*day,value:null},{at:0,value:10},{at:NaN,value:2}]);
  assert.equal(model.points.length,2);assert.equal(model.points[0].value,5);assert.equal(model.points[1].value,0);assert.equal(model.change,-5);
});
test('空数据或单点不生成净增量，固定数值也有可绘制范围',()=>{
  assert.equal(trendModel([]).change,null);assert.equal(trendModel([{at:t,value:2}]).change,null);
  const constant=trendModel([{at:t,value:0},{at:t+day,value:0}]);assert.equal(constant.change,0);assert(constant.min<0&&constant.max>0);
});
