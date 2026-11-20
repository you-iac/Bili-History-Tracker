// images.test.mjs 验证图片URL来源边界与快照解析，避免动态图片变成任意URL加载器。
import test from 'node:test';
import assert from 'node:assert/strict';
import {safeImageURL,snapshotImageURL} from './images.js';

test('B站图片URL可升级HTTPS并保留图片处理参数',()=>{
  assert.equal(safeImageURL('http://i0.hdslb.com/bfs/face/avatar.jpg@96w_96h#x'),'https://i0.hdslb.com/bfs/face/avatar.jpg@96w_96h');
  assert.equal(safeImageURL('//archive.biliimg.com/bfs/archive/cover.jpg?x=1'),'https://archive.biliimg.com/bfs/archive/cover.jpg?x=1');
});
test('拒绝其他域名、伪装子域名、凭据及本地文件协议',()=>{
  for(const raw of ['https://i0.hdslb.com.evil.test/x','https://evilhdslb.com/x','https://127.0.0.1/x','https://u:p@i0.hdslb.com/x','https://i0.hdslb.com:8443/x','data:image/svg+xml,x','javascript:alert(1)','file:///tmp/a','/local-image','https://i0.hdslb.com\\@evil.test/x','https://i0.hdslb.com/x\ny'])assert.equal(safeImageURL(raw),'',raw);
});
test('分别读取UP头像和视频封面，不把视频作者头像用作封面',()=>{
  assert.equal(snapshotImageURL('creator',{card:{face:'https://i0.hdslb.com/bfs/face/a.jpg'}}),'https://i0.hdslb.com/bfs/face/a.jpg');
  assert.equal(snapshotImageURL('video',{pic:'https://i1.hdslb.com/bfs/archive/c.jpg',owner:{face:'https://i0.hdslb.com/bfs/face/a.jpg'}}),'https://i1.hdslb.com/bfs/archive/c.jpg');
  assert.equal(snapshotImageURL('video',{owner:{face:'https://i0.hdslb.com/bfs/face/a.jpg'}}),'');
});
