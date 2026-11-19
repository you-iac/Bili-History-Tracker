// detail_browser_test.go 可选地让真实浏览器通过完整 Agent HTTP/缓存/远程客户端链路回归。
// 平台与远端均为桩，独立本地状态；需显式提供 Node/CDP 驱动，不访问真实 Cookie。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/library"
	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
	"bili-history-tracker/client/agent/internal/scanner"
	"bili-history-tracker/client/frontend"
)

type browserMetadata struct{ mode *atomic.Int32 }

func (p browserMetadata) Current(_ context.Context, k, id string) (bilibili.Metadata, error) {
	if p.mode.Load() != 0 {
		return bilibili.Metadata{}, errors.New("platform unavailable")
	}
	zero := int64(0)
	return bilibili.Metadata{Kind: k, ID: id, Name: "Live " + id, Metric: &zero, ObservedAt: time.Now().UnixMilli()}, nil
}

func TestBrowserDetailWithRealAgent(t *testing.T) {
	driver, node := os.Getenv("BHT_DETAIL_BROWSER_DRIVER"), os.Getenv("BHT_DETAIL_NODE")
	urlFile := os.Getenv("BHT_DETAIL_BROWSER_URL_FILE")
	if urlFile == "" && (driver == "" || node == "") {
		t.Skip("explicit local browser integration")
	}
	var mode atomic.Int32
	var unexpected atomic.Int32
	resultReady := make(chan struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if r.Method != "GET" || len(parts) < 4 || parts[0] != "api" || parts[1] != "detail" {
			unexpected.Add(1)
			w.WriteHeader(500)
			return
		}
		if mode.Load() == 2 {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"server unavailable"}`))
			return
		}
		id := parts[3]
		if len(parts) == 5 && parts[4] == "saved" {
			json.NewEncoder(w).Encode(map[string]any{"item": remote.Item{Kind: "creator", ID: id, Name: "Saved " + id, Seq: 5, Recorded: true, ObservedAt: 1000}, "description": "saved description", "snapshot_at": 500})
			return
		}
		if len(parts) == 5 && parts[4] == "versions" {
			json.NewEncoder(w).Encode(map[string]any{"kind": "creator", "id": id, "items": []remote.Version{{Seq: 5, At: 500}, {Seq: 4, At: 400}}, "has_more": false, "next_before": 0})
			return
		}
		if len(parts) == 5 && parts[4] == "snapshot" {
			fmt.Fprintf(w, `{"kind":"creator","id":%q,"seq":%s,"at":500,"snapshot":{"card":{"name":"snapshot"}}}`, id, r.URL.Query().Get("seq"))
			return
		}
		json.NewEncoder(w).Encode(remote.History{Record: remote.Record{Kind: "creator", ID: id, Recorded: true, Seq: 5, ObservedAt: 1000}, Target: remote.Status{Kind: "creator", ID: id, Recorded: true, TrackingState: "paused", State: "paused"}, Metrics: remote.Series{Points: []remote.Point{{At: 0, Value: 0}, {At: 86400000, Value: 2}}}})
	}))
	defer upstream.Close()
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scan, err := scanner.New(platformStub{}, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Close()
	collection, err := library.New(store, remote.New(nil), browserMetadata{&mode})
	if err != nil {
		t.Fatal(err)
	}
	if err = collection.Configure(upstream.URL); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	agent, err := New(scan, server.Listener.Addr().String(), collection, store)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/browser-test":
			page, _ := frontend.Files.ReadFile("index.html")
			text := strings.Replace(string(page), `<script type="module" src="/app.js"></script>`, "", 1)
			text = strings.Replace(text, "</body>", "<script type=\"module\" src=\"/browser-test.mjs\"></script></body>", 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(text))
		case "/browser-mode":
			mode.Store(int32(len(r.URL.Query().Get("mode"))))
			w.WriteHeader(204)
		case "/browser-result":
			var result struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&result) != nil {
				w.WriteHeader(400)
				return
			}
			select {
			case resultReady <- result:
			default:
			}
			w.WriteHeader(204)
		case "/browser-test.mjs":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, browserAgentScript, agent.BootstrapToken())
		default:
			agent.ServeHTTP(w, r)
		}
	})
	server.Start()
	defer server.Close()
	if urlFile != "" {
		// 外部驱动模式用于禁止Go测试子进程启动Chrome的Windows作业环境。
		if err = os.WriteFile(urlFile, []byte(server.URL+"/browser-test"), 0600); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-resultReady:
			if !result.OK {
				t.Fatal(result.Error)
			}
		case <-time.After(40 * time.Second):
			t.Fatal("external browser driver timed out")
		}
		if unexpected.Load() != 0 {
			t.Fatal("browser used legacy detail or writes")
		}
		return
	}
	out := os.Getenv("BHT_DETAIL_BROWSER_REPORT")
	if out == "" {
		out = t.TempDir()
	}
	if err = os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := exec.CommandContext(ctx, node, driver, server.URL+"/browser-test", "390", out).CombinedOutput()
	if err != nil {
		t.Fatalf("browser integration: %v\n%s", err, result)
	}
	if unexpected.Load() != 0 {
		t.Fatal("browser used legacy detail or writes", unexpected.Load())
	}
	t.Log(string(result))
}

const browserAgentScript = `
import {initDetail} from '/detail.js';
import {initImages} from '/images.js';
const result=document.createElement('pre');result.id='test-results';result.hidden=true;document.body.append(result);
const check=(ok,message)=>{if(!ok)throw Error(message);};
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<300;i++){if(fn())return;await pause(10);}throw Error('wait timeout');}
try{
 const auth=await fetch('/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:%q})});check(auth.ok,'session failed');
 const calls=[];const api=async(path,options)=>{calls.push(path);const response=await fetch(path,{credentials:'same-origin',...options});if(!response.ok)throw Error('Agent HTTP '+response.status);return response.json();};
 for(const el of document.querySelectorAll('[data-page]'))el.hidden=true;document.getElementById('detail-panel').hidden=false;document.getElementById('connect-card').hidden=true;
 const detail=initDetail({api,notify:()=>{},onAdd:async()=>{},onChanged:async()=>{},images:initImages({api})});detail.setConnected(true);
 const route=id=>({kind:'creator',id,section:'creators'});
 await detail.show(route('123'));
 check(document.getElementById('detail-name').textContent==='Live 123','live profile missing');check(document.getElementById('detail-total-versions').textContent==='v5','record seq missing');
 check(!calls.some(p=>p.includes('/snapshot')||p.includes('/versions')||p.includes('/images/')),'eager additional read');
 document.getElementById('detail-history').open=true;await until(()=>document.querySelectorAll('#detail-versions button').length===2);
 document.querySelector('#detail-versions button').click();await until(()=>!document.getElementById('detail-compare').disabled);document.getElementById('detail-compare').click();await until(()=>!document.getElementById('detail-compare').disabled);
 await fetch('/browser-mode?mode=x');document.getElementById('detail-refresh').click();await until(()=>document.getElementById('detail-name').textContent==='Saved 123');
 check(document.getElementById('detail-source').textContent.includes('服务器最后记录 v5'),'fallback provenance missing');
 await fetch('/browser-mode?mode=xx');await detail.show(route('124'));check(document.getElementById('detail-source').textContent.includes('暂不可获取'),'missing data state wrong');check(document.getElementById('detail-total-versions').textContent==='未知','failed history marked absent');
 result.textContent=JSON.stringify({ok:true,width:innerWidth,requests:calls.length,checks:['real-agent-session','platform','history','lazy-versions','explicit-compare','saved-fallback','both-failed','unknown-record']});
}catch(error){result.textContent=JSON.stringify({ok:false,error:error.stack});}
await fetch('/browser-result',{method:'POST',headers:{'Content-Type':'application/json'},body:result.textContent});
`
