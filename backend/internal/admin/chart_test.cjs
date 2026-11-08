// 原生趋势图的数据边界测试，运行 node --test internal/admin/chart_test.cjs。
const test=require('node:test');
const assert=require('node:assert/strict');
const {segments,summary}=require('./assets/app.js');
const day=86400000;
test('缺失日切断曲线，不生成零值或跨缺口连线',()=>{
 const points=[{at:day,value:100},{at:2*day,value:120},{at:5*day,value:180}];
 assert.deepEqual(segments(points).map(group=>group.length),[2,1]);assert.equal(points.length,3);
});
test('空数据和单点不伪造增长',()=>{
 assert.equal(summary([]).delta,null);assert.equal(summary([{at:day,value:50}]).delta,null);
});
test('真实回落显示为负值，零增长仍是有效增量',()=>{
 assert.equal(summary([{at:day,value:120},{at:2*day,value:100}]).delta,-20);
 assert.equal(summary([{at:day,value:0},{at:2*day,value:0}]).delta,0);
});
