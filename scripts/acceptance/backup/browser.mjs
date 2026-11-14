// Dependency-free CDP driver for the actual B04 artifact's rendered UI.
// A fresh browser profile and loopback debugger isolate all acceptance state.
import fs from 'node:fs';
import path from 'node:path';
import {spawn} from 'node:child_process';
import net from 'node:net';

const [rootArg, action, argument, value] = process.argv.slice(2);
const root = path.resolve(rootArg);
const statePath = path.join(root, 'browser-state.json');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function require(test, message) { if (!test) throw new Error(message); }

async function openCDP(url) {
  const socket = new WebSocket(url);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, {once:true}); socket.addEventListener('error', reject, {once:true}); });
  let serial = 0;
  const pending = new Map();
  const responses = [];
  socket.addEventListener('message', event => {
    const data = JSON.parse(event.data);
    if(data.method==='Network.responseReceived') {
      const response=data.params.response, url=new URL(response.url);
      if(url.protocol==='http:'&&url.hostname==='127.0.0.1') responses.push({path:url.pathname,status:response.status});
    }
    if (!data.id || !pending.has(data.id)) return;
    const {resolve, reject, timer} = pending.get(data.id);
    pending.delete(data.id); clearTimeout(timer);
    data.error ? reject(new Error(data.error.message)) : resolve(data.result);
  });
  return {responses,close:()=>socket.close(), send:(method, params={})=>new Promise((resolve,reject)=>{
    const id=++serial;
    const timer=setTimeout(()=>{pending.delete(id);reject(new Error('CDP timed out: '+method));},20000);
    pending.set(id,{resolve,reject,timer}); socket.send(JSON.stringify({id,method,params}));
  })};
}

if (action === 'start') {
  require(!fs.existsSync(root), 'fresh browser evidence directory required');
  fs.mkdirSync(root,{recursive:true});
  const executable=process.env.B04_BROWSER_EXECUTABLE;
  require(executable && fs.existsSync(executable), 'installed browser path required');
  const listener=net.createServer();
  await new Promise(resolve=>listener.listen(0,'127.0.0.1',resolve));
  const port=listener.address().port;
  await new Promise(resolve=>listener.close(resolve));
  const output=fs.openSync(path.join(root,'browser-process.log'),'wx');
  const child=spawn(executable,['--headless=new','--remote-debugging-address=127.0.0.1',`--remote-debugging-port=${port}`,`--user-data-dir=${path.join(root,'profile')}`,'--no-first-run','--no-default-browser-check','--window-size=1440,1000','about:blank'],{stdio:['ignore',output,output],windowsHide:true,detached:true});
  child.unref(); fs.closeSync(output);
  let targets;
  for(let attempt=0;attempt<100;attempt++) {
    try { targets=await (await fetch(`http://127.0.0.1:${port}/json/list`)).json(); if(targets.some(item=>item.type==='page'))break; }catch{}
    await sleep(200);
  }
  const target=targets?.find(item=>item.type==='page');
  require(target, 'isolated browser did not start');
  const version=await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  fs.writeFileSync(statePath,JSON.stringify({port,pid:child.pid,target:target.webSocketDebuggerUrl,browser:version.Browser,browserSocket:version.webSocketDebuggerUrl},null,2)+'\n',{flag:'wx'});
  console.log(JSON.stringify({started:true,browser:version.Browser,loopback:true}));
  process.exit(0);
}

const state=JSON.parse(fs.readFileSync(statePath,'utf8'));
const client=await openCDP(action==='close'?state.browserSocket:state.target);
if(action!=='close') { await client.send('Page.bringToFront'); await client.send('Network.enable'); }
async function evaluate(expression) {
  const response=await client.send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});
  require(!response.exceptionDetails, 'browser evaluation failed');
  return response.result.value;
}
try {
  let result;
  if(action==='navigate') {
    const url=new URL(argument);
    require(url.protocol==='http:' && url.hostname==='127.0.0.1', 'only isolated loopback products allowed');
    await client.send('Page.navigate',{url:url.href});
    await sleep(1000);
    result={navigated:true};
  } else if(action==='snapshot') {
    result=await evaluate(`(() => {
      const visible=e=>{const r=e.getBoundingClientRect();return r.width>0&&r.height>0&&getComputedStyle(e).visibility!=='hidden'&&!!e.checkVisibility({opacityProperty:true,visibilityProperty:true});};
      const controls=[...document.querySelectorAll('button,input,textarea,select,[role="tab"],[role="checkbox"],[role="switch"],[role="combobox"]')].filter(visible).map((e,i)=>{
        e.dataset.b04Ref=String(i);
        return {ref:i,tag:e.tagName,role:e.getAttribute('role'),type:e.getAttribute('type'),text:(e.innerText||'').slice(0,120),label:e.getAttribute('aria-label')||[...(e.labels||[])].map(l=>l.innerText).join(' '),placeholder:e.getAttribute('placeholder'),value:e.type==='password'?'[redacted]':e.type==='number'?e.value:undefined};
      });
      return {title:document.title,visibility:document.visibilityState,text:document.body.innerText.slice(0,18000),controls};
    })()`);
  } else if(action==='click' || action==='fill') {
    require(/^\d+$/.test(argument), 'snapshot control reference required');
    const selector=`[data-b04-ref="${argument}"]`;
    const location=await evaluate(`(() => {const e=document.querySelector(${JSON.stringify(selector)});if(!e)throw Error('missing ref');e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2};})()`);
    await client.send('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...location});
    await client.send('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...location});
    if(action==='fill') {
      await client.send('Input.dispatchKeyEvent',{type:'keyDown',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});
      await client.send('Input.dispatchKeyEvent',{type:'keyUp',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});
      await client.send('Input.insertText',{text:value});
    }
    await sleep(600); result={action,ref:Number(argument),completed:true};
  } else if(action==='upload') {
    require(/^\d+$/.test(argument), 'snapshot file control reference required');
    const file=path.resolve(value);
    const evidenceRoot=path.resolve('.tmp-b04-20261113-01')+path.sep;
    require(file.startsWith(evidenceRoot)&&fs.existsSync(file), 'B04 synthetic fixture required');
    const document=await client.send('DOM.getDocument');
    const node=await client.send('DOM.querySelector',{nodeId:document.root.nodeId,selector:`input[type="file"][data-b04-ref="${argument}"]`});
    require(node.nodeId, 'file input reference missing');
    await client.send('DOM.setFileInputFiles',{nodeId:node.nodeId,files:[file]});
    result={uploaded:path.basename(file)};
  } else if(action==='screenshot') {
    require(/^[a-z0-9-]+\.png$/.test(argument), 'safe screenshot name required');
    const capture=await client.send('Page.captureScreenshot',{format:'png',captureBeyondViewport:true});
    fs.writeFileSync(path.join(root,argument),Buffer.from(capture.data,'base64'),{flag:'wx'});
    result={screenshot:argument};
  } else if(action==='close') {
    await client.send('Browser.close'); result={closed:true};
  } else throw new Error('unsupported action');
  if(action==='snapshot') {
    fs.writeFileSync(path.join(root,`snapshot-${Date.now()}.json`),JSON.stringify(result,null,2)+'\n',{flag:'wx'});
  }
  fs.appendFileSync(path.join(root,'commands.jsonl'),JSON.stringify({action,argument:action==='navigate'?new URL(argument).pathname:argument,result:action==='snapshot'?{visibility:result.visibility,controls:result.controls.length}:result,responses:client.responses,time:new Date().toISOString()})+'\n');
  if(client.responses.length) result.responses=client.responses;
  console.log(JSON.stringify(result,null,2));
} finally { client.close(); }
