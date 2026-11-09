// 验证跨页、筛选和导入完成时的选择状态，运行 node --test internal/admin/discovery_test.cjs。
const test=require('node:test'),assert=require('node:assert/strict');
const {Selection}=require('./assets/discovery.js');
test('加载结果默认不选择，跨页去重并保留既有选择',()=>{const m=new Selection();m.merge([{id:'a',title:'A'},{id:'b',title:'B'}]);assert.equal(m.selected.size,0);m.toggle('a',true);m.merge([{id:'a',title:'new A'},{id:'c',title:'C'}]);assert.equal(m.items.size,3);assert.deepEqual([...m.selected],['a']);assert.equal(m.rows('new')[0].id,'a');assert.deepEqual([...m.selected],['a']);});
test('导入成功项移出添加列表，失败和拒绝项保留',()=>{const m=new Selection();m.merge(['a','b','c'].map(id=>({id,title:id})));for(const id of m.items.keys())m.toggle(id,true);m.imported([{id:'a',status:'added'},{id:'b',status:'failed'},{id:'c',status:'rejected'}]);assert.deepEqual([...m.items.keys()],['b','c']);assert.deepEqual([...m.selected],['b','c']);});
