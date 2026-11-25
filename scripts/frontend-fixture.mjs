import { createServer } from 'vite';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const at = Date.UTC(2026, 10, 26, 0, 0);
const models = ['gpt-6.2-astra','gpt-6.2-sol','claude-fable-5-2','gemini-4-hydrogen'];
const sources = ['华东 A', '备用来源 B', '名称很长的跨区域演示来源'].map((name,i) => ({id:'source-'+(i+1),name,base:'https://long-namespace.gateway-'+(i+1)+'.example.test/v1/production-configuration',has_storage: true}));
const keys = sources.flatMap(s => [{key_id:s.id+'::demo',source_id:s.id,source_name:s.name,position:1,prefix:'demo-…1234'}]);
const longName = '渠道-A-very-long-provider-west-europe-2026-long-name-for-stress-test';
const baseStats = {started:0,success:0,failed:0,success_rate_denominator:0,success_rate:null,inflight:null,skipped:0,client_cancelled:0,hedge_cancelled:0,last_success_at:null};
const channels = Array.from({length:30},(_,i)=>{
  const s=sources[i%3], model=models[i%4], provider=i===0?longName:'渠道-'+String(i+1).padStart(2,'0');
  return {source_id:s.id,source_name:s.name,label:provider,provider,model,upstream_model:i===0?model+'-very-long-upstream-model-identifier-2026':model,endpoint:'all',stream:null,eligible:i!==3,reason:i===3?'cooldown':'eligible',engine:'gpt',stats:i===2?baseStats:{...baseStats,started:2460-i*9,success:2312-i*8,failed:148-i,success_rate_denominator:2460-i*9,success_rate:0.94-i*.004,inflight:i%6,input_tokens:1234567,output_tokens:199002,cache_read_tokens:763226,usage_samples:2432,estimated_cost_usd:10.65+i/17,last_success_at:Math.floor(at/1000)-i*32}};
});
const managed = channels.map(c=>({...c,name:c.provider,kind:'configured',account_id:'',group_id:0,account_ids:[],base:'https://mock-gateway.example.test/v1',api_key_id:'',key_position:0,key_prefix:'',positions:{},revision:'r1',manageable:true,models:[c.model],model_mappings:{[c.model]:c.upstream_model},probe_fingerprint:'audit-only'}));
const testAccount = {id:'wallet-fixture',name:'跨区域演示钱包账号',base:'https://long-wallet-site.example.test/account-management',email:'visual-audit@example.test',provider_kind:'sub2api',state:'idle',message:'',synced_at:Math.floor(at/1000),targets:[]};
const logs = Array.from({length:16},(_,i)=>({event_id:'evt-'+i,source_id:sources[i%3].id,instance_id:'fixture-instance',request_id:'request-audit-a-long-deliberate-trace-id-for-layout-review-'+String(i).padStart(4,'0'),trace_id:'trace-'+i,key_id:'demo',model:models[i%4],endpoint:'/v1/responses',stream:i%4!==3,at_ms:at-i*7300,started_ms:at-i*7300-3900,status:i%3===1?502:200,outcome:i%3===1?'failed':'success',result:i%3===1?'failed':'success',failure_reason:i%3===1?'missing_response_completed':'',response_headers_status:200,duration_ms:3901+i*377,dispatch_ms:88+i*8,response_created_ms:344+i*11,first_text_ms:i%4!==3?1500+i*127:null,input_tokens:19293+i*367,output_tokens:548+i*208,cache_read_tokens:9810}));
let managementRows;
const authCookie = 'visual_audit_local=1';
const allowed = (req,u)=>new URL(req.url,'http://127.0.0.1').pathname===u;
const plugin = {
  name:'local-visual-fixture',
  configureServer(server) {
    server.middlewares.use((req,res,next)=>{
      const url = new URL(req.url,'http://127.0.0.1');
      const path = url.pathname;
      if (!path.startsWith('/analytics/') && !path.startsWith('/_audit/')) return next();
      const json = (body, status=200) => {res.writeHead(status,{'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store'});res.end(JSON.stringify(body));};
      if (allowed(req,'/_audit/session') || path.endsWith('/auth/login')) {res.setHeader('Set-Cookie',authCookie+'; Path=/; SameSite=Strict');if(path.startsWith('/_audit/')){res.writeHead(302,{Location:'/#view=overview'});return res.end();}return json({ok:true});}
      if (path.endsWith('/auth/logout')) {res.setHeader('Set-Cookie','visual_audit_local=; Max-Age=0; Path=/');return json({ok:true});}
      if (path.endsWith('/auth/me')) return json({enabled:true,authenticated:req.headers.cookie?.includes(authCookie)??false,username:'audit-admin'});
      if (req.method !== 'GET' && req.method !== 'HEAD') return json({message:'审查夹具不执行写入'},409);
      const sid = url.searchParams.get('source_id') || /\/sources\/(source-\d+)\//.exec(path)?.[1];
      const scope = rows => sid?rows.filter(r=>r.source_id===sid):rows;
      if (path.endsWith('/sources')) return json({data:sources});
      if (path.endsWith('/api-keys')) return json({data:scope(keys),can_inspect_all:true});
      if (path.endsWith('/model-channels') || path.endsWith('/channel-metrics')) return json({data:scope(channels),snapshot_revision:'r1',generated_at:Math.floor(at/1000)});
      if (path.endsWith('/analytics')) return json({data:scope(channels),total:{requests:25391,attempts:33011,success:31740,failed:1271,input_tokens:32901122,output_tokens:3918242,cache_read_tokens:23045018,estimated_cost_usd:431.289}, models:[],from:Math.floor(at/1000)-86400,to:Math.floor(at/1000),generated_at:Math.floor(at/1000),coverage:'full',source_freshness:sources.map(s=>({source_id:s.id,latest_fact_at:Math.floor(at/1000)-5,last_exporter_seen_at:Math.floor(at/1000)-4}))});
      if (path.endsWith('/key-request-stats')) return json({data:scope(keys).map(k=>({...k,key_id:'demo',requests:3200,success:3140,failed:60})),total:{requests:9600,success:9420,failed:180},import:{caught_up:true}});
      if (path.endsWith('/request-logs')) return json({data:logs,models,next_cursor:'page-2',from:at-86400000,to:at,import:{caught_up:true}});
      if (path.endsWith('/request-trace')) {const l=logs.find(x=>x.request_id===url.searchParams.get('request_id'))||logs[1];return json({import:{caught_up:true},data:[{source_id:l.source_id,instance_id:l.instance_id,request_id:l.request_id,ambiguous:false,events:[{...l,kind:'request',stage:'',provider:'',upstream_model:'',response_completed:false,attempt_id:'',detail:{},transport:{}},{...l,event_id:'attempt-1',kind:'attempt',stage:'',provider:longName,upstream_model:channels[0].upstream_model,attempt_id:'attempt-1',detail:{},transport:{}}]}]});}
      if (path.endsWith('/channel-management/query')) {
        const rows=managementRows([],managed,[],'','',[]);
        const offset=Number(url.searchParams.get('cursor')||0),page=rows.slice(offset,offset+25);
        return json({format:'management-page-v1',revision:'fixture-only',read_at_ms:Date.now(),rows:page,total:rows.length,eligible_count:rows.length,offset,page_size:25,next_cursor:offset+25<rows.length?'25':undefined,previous_cursor:offset?'0':undefined,selection_token:'fixture-only',accounts:[testAccount],rates:[],platforms:['gpt'],models,detection_models:models,context:{prices:[],imports:[],routes:{},fingerprints:Object.fromEntries(page.map(r=>[r.id,[]])),fingerprint_models:models}});
      }
      if (path.endsWith('/channel-management')) return json({data:scope(managed),unavailable_sources:[]});
      if (path.endsWith('/sub2api/accounts')) return json({data:[testAccount]});
      if (path.endsWith('/sub2api/accounts/progress')) return json({data:[]});
      if (path.endsWith('/accounts/wallet-fixture/balance')) return json({status:'ok',amount:28.250438,checked_at:Math.floor(at/1000),provider_kind:'sub2api'});
      if (path.endsWith('/channel-balances')) return json({status:'unsupported'});
      if (path.endsWith('/status')) return json({enabled:true,ready:true,data:[]});
      return json({data:[],labels:{},unavailable_sources:[]});
    });
  }
};
const cacheDir = resolve(process.env.UNI_VISUAL_CACHE || tmpdir(), 'uni-console-visual-vite');
const server = await createServer({ root, plugins: [plugin], cacheDir, server: { host: '127.0.0.1', port: 43971, strictPort: true, fs: {allow:[root, cacheDir]} } });
({managementRows}=await server.ssrLoadModule('/src/channelManagement.ts'));
await server.listen();
console.log('Local visual fixture: http://127.0.0.1:43971');
