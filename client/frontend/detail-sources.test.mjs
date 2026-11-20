// detail-sources.test.mjs 验证首次读取预算、缓存竞态和资料/历史局部失败。
import test from 'node:test';
import assert from 'node:assert/strict';
import {readCreatorSources} from './detail-sources.mjs';

test('作者首屏只读缓存、当前资料和历史，不请求版本或视频摘要',async()=>{
  const paths=[],current=[],history=[],errors=[];
  await readCreatorSources({api:async path=>{paths.push(path);return path.includes('/cache')?{source:'cache'}:path.includes('/current/')?{source:'platform'}:{record:{seq:7}};},id:'123',onCurrent:v=>current.push(v),onHistory:v=>history.push(v),onError:(...e)=>errors.push(e)});
  assert.deepEqual(paths,['/api/current/creator/123/cache','/api/current/creator/123','/api/history/creator/123?days=30']);
  assert.equal(current.at(-1).source,'platform');assert.equal(history[0].record.seq,7);assert.deepEqual(errors,[]);
});
test('迟到的旧缓存不能覆盖新资料，历史失败不阻止平台资料显示',async()=>{
  let release;const cache=new Promise(resolve=>release=resolve),values=[],errors=[];
  const read=readCreatorSources({api:async path=>{if(path.endsWith('/cache'))return cache;if(path.includes('/history/'))throw Error('offline');return {source:'platform'};},id:'123',onCurrent:v=>values.push(v),onHistory:()=>assert.fail(),onError:(part)=>errors.push(part)});
  await new Promise(resolve=>setImmediate(resolve));release({source:'old-cache'});await read;
  assert.deepEqual(values,[{source:'platform'}]);assert.deepEqual(errors,['history']);
});
test('切换时间范围只读历史；当前资料失败仍可展示历史',async()=>{
  const paths=[],errors=[],histories=[];
  await readCreatorSources({api:async p=>{paths.push(p);return {};},id:'123',days:'7',historyOnly:true,onCurrent:()=>assert.fail(),onHistory:v=>histories.push(v),onError:()=>assert.fail()});
  assert.deepEqual(paths,['/api/history/creator/123?days=7']);
  await readCreatorSources({api:async p=>{if(p.includes('/current/'))throw Error('unavailable');return {record:{seq:1}};},id:'123',onCurrent:()=>assert.fail(),onHistory:v=>histories.push(v),onError:part=>errors.push(part)});
  assert.equal(histories.length,2);assert.deepEqual(errors,['current']);
});
