import {$,cl,lin,eo,eio,P,PI,mix,show,stream,setHTML,IC,I} from './foundation.js';
import {productIcons} from './icons.js';
import {captions} from './narration.js';
export {captions};
import {extraScenes,extraInputs,mountExtraScenes,renderExtraScene} from './knowledge-scenes.js';

// The original intro-film typography, HTML UI, timed reveals and camera language
// are reused here. All scenes below are newly authored for the v0.9.0 baseline.
Object.assign(IC,{
 assistant:`<g transform="scale(1.2)">${productIcons.assistant}</g>`,
 clock:'<circle cx="12" cy="12" r="9"/><path d="M12 6v6l4 2"/>',
 automation:`<g transform="scale(1.2)">${productIcons.automation}</g>`,
 shield:'<path d="m12 3 8 3v6c0 5-4 8-8 10-4-2-8-5-8-10V6z"/><path d="m8 12 3 3 5-6"/>',
 plus:'<path d="M12 4v16M4 12h16"/>', close:'<path d="m6 6 12 12M18 6 6 18"/>',
 up:'<path d="M12 20V4M5 11l7-7 7 7"/>',
 help:'<circle cx="12" cy="12" r="9"/><path d="M9.5 9a2.5 2.5 0 1 1 4 2c-1 .6-1.5 1-1.5 2M12 17h.01"/>',
 pin:'<path d="m9 3 8 1-2 5 3 4-4 1-5 5-4-4 5-5-1-4zM8 16l-5 5"/>',
 chip:'<rect x="5" y="5" width="14" height="14" rx="2"/><path d="M8 1v4M16 1v4M8 19v4M16 19v4M1 8h4M1 16h4M19 8h4M19 16h4"/>',
 plug:'<path d="M8 2v6M16 2v6M5 8h14v3a7 7 0 0 1-14 0zM12 18v5"/>',
 play:'<path d="m8 5 11 7-11 7z"/>', eye:'<path d="M2 12S6 5 12 5s10 7 10 7-4 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
});

export const duration=165;
export const scenes=[
 {a:0,b:22,key:'intro',label:'01 — WEKNORA ASSISTANT',title:'Ask about WeKnora.<br>Work with your page.',lede:'Get product answers, then act on the knowledge and tools already in your workspace.'},
 {a:22,b:47,key:'create',label:'02 — CREATE AN AGENT',title:'Your request.<br>A configured agent.',lede:'Start from the Agents page. Describe the job; the Assistant connects the resources.'},
 {a:47,b:65,key:'collaborate',label:'03 — MULTI-AGENT COLLABORATION',title:'One request.<br>A team at work.',lede:'Split the task, follow each agent and bring the findings together.'},
 {a:65,b:87,key:'automate',label:'04 — AUTOMATIONS',title:'A task today.<br>A routine tomorrow.',lede:'Start with a request and a time. Let the Assistant build the recurring task.'},
 {a:87,b:101,key:'approve',label:'05 — EXECUTION PERMISSIONS',title:'Review the change.<br>Then let it run.',lede:'Choose how the assistant can act. Every operation stays within your own access.'},
 {a:101,b:113,key:'background',label:'06 — BACKGROUND TASKS',title:'Keep the<br>work moving.',lede:'Run longer commands in the sandbox. Follow progress and open the finished file.'},
 ...extraScenes,
 {a:161,b:165,key:'close',title:'WeKnora v0.9.0'},
];
export const interactions=[
 {scene:'intro',start:5.7,end:7.8,send:8.15,prompt:"What's new in WeKnora v0.9.0? What can the Assistant help me do?"},
 {scene:'create',start:1.7,end:4.4,send:4.75,prompt:'Create a reading-club coordinator using my existing knowledge, model, MCP tools and skills'},
 {scene:'collaborate',start:.7,end:3.4,send:3.75,prompt:'Prepare the reading-club briefing. Split the agenda, sign-up rules and preparation checks across agents.'},
 {scene:'automate',start:1.7,end:4.4,send:4.75,prompt:'Every weekday at 9 AM, check the reading-club plan and send me a preparation brief'},
 {scene:'approve',start:1.9,end:4.5,send:4.85,prompt:'Rename Weekend reading club to Reading club workspace. Ask me before changing it.'},
 {scene:'background',start:.65,end:2.8,send:3.15,prompt:'Build a Markdown run sheet from the reading-club documents.'},
 ...extraInputs,
];
// Character events are the single source of truth for both the picture and audio.
// Small pauses at word boundaries retain the rhythm of typing without a uniform buzz.
export const keyEvents=[];
for(const input of interactions){
 const weights=[...input.prompt].map((c,k)=>(c===' '?1.45:/[,.?]/.test(c)?2.1:.82+(k*7%11)*.035));
 const total=weights.reduce((a,b)=>a+b,0);let sum=0;
 input.chars=weights.map((w,k)=>{sum+=w;return {at:Math.round((input.start+(input.end-input.start)*sum/total)*60)/60,char:input.prompt[k]}});
 const offset=scenes.find(s=>s.key===input.scene).a;
 for(const c of input.chars)keyEvents.push({t:+(offset+c.at).toFixed(6),char:c.char,scene:input.scene});
}


const esc=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;');
const cite=title=>`<span class="cite">${I('book')}${title}</span>`;
const user=(key,prompt)=>`<div class="umsg" id="${key}U"><div>${prompt}</div></div>`;
const step=(key,n,ic,title,desc='')=>`<div class="step" id="${key}Step${n}"><div class="si">${I(ic)}</div><div class="st">${title}${desc?`<small>${desc}</small>`:''}</div></div>`;
const cursorSVG='<svg viewBox="0 0 30 34"><path d="M3 2 25 17 15 18 11 30Z" fill="#30343a" stroke="white" stroke-width="1.8" stroke-linejoin="round"/></svg>';
const welcome=key=>`<div class="welcome" id="${key}Welcome">${I('assistant')}<h2>How can I help?</h2><p>Ask about WeKnora, or get help with the page you are on.</p></div>`;
const composer=key=>`<div class="inbox" id="${key}InputBox"><div class="ph" id="${key}In" ${['create','automate'].includes(key)?'':`data-typewriter="${key}" data-chars="0"`}></div><div class="bar">${I('plus')}<span class="access-trigger" id="${key}Access">${I('shield')}Ask first ${I('chevd')}</span><span class="send"><span class="model">deepseek-v4-flash</span><i id="${key}Send">${I('up')}</i></span></div></div>`;
const approval=(key,title,fields)=>`<div class="approval" id="${key}Approval"><div class="approval-label">${I('shield')}Approval required</div><h3>${title}</h3><dl>${fields.map(([k,v])=>`<dt>${k}</dt><dd>${v}</dd>`).join('')}</dl><footer><small>Waiting for your decision</small><span class="btn">Reject</span><span class="btn primary" id="${key}Approve">Approve &amp; run</span></footer></div>`;
const nativeCard=(ic,title,meta,tags=[])=>`<div class="native-card"><div class="card-title">${I(ic)}${title}</div><div class="card-meta">${meta}</div>${tags.length?`<div class="tags">${tags.map(x=>`<span>${x}</span>`).join('')}</div>`:''}</div>`;
const content={};

const VERSION_ANSWER=`<p>Here’s what’s new in <b>WeKnora v0.9.0</b>.</p><h3>WeKnora Assistant</h3><p>Ask about releases, features, setup and troubleshooting. The Assistant can also use your current page to create agents, manage knowledge and configure tasks.</p><h3>Multi-agent collaboration</h3><p>Delegate work to named agents, follow their progress and combine their results in one conversation.</p><h3>Automations</h3><p>Schedule work in plain language or set it up manually. The Assistant runs the task as you and notifies you about results or approvals.</p><h3>Execution permissions</h3><p>Choose <b>Read only</b>, <b>Ask first</b> or <b>Auto</b>. Resource permissions still apply; sensitive operations can require confirmation.</p><h3>Background tasks</h3><p>Keep longer sandbox commands running and return to their results when they finish.</p><p>${cite('v0.9.0 release notes')}${cite('WeKnora Assistant')}</p>`;
content.intro=user('intro',interactions[0].prompt)+`<div class="sum" id="introSum">${I('bulb')}I’ll check the release notes and Assistant guide.</div><div id="introSteps">`+
step('intro',0,'search','Search product docs','“v0.9.0 release notes and WeKnora Assistant”')+
step('intro',1,'fileq','Read product doc','v0.9.0 release notes · Features and upgrade notes')+
step('intro',2,'fileq','Read product doc','WeKnora Assistant · Capabilities and user identity')+
step('intro',3,'check','Documentation checked')+`</div><div class="ans" id="introAns"></div>`;

const RESOURCES=[
 ['search','Find knowledge bases','Weekend reading club · 3 documents'],
 ['chip','List available models','deepseek-v4-flash · Chat model'],
 ['plug','List MCP services','Event workspace · read_event'],
 ['skill','List installed skills','brief-writer · Briefing structure and citations'],
];
// The expanded requests match assistant.prompts in frontend/src/locales/en-US.ts.
const assembled={
 create:`Create an agent for this request: ${interactions[1].prompt}. Check available models, knowledge bases, MCP services and skills; choose suitable resources. Ask only for necessary missing information, follow the current access policy, and open its editor after creation.`,
 automate:`Create a scheduled task: ${interactions[3].prompt}. Use timezone Asia/Shanghai unless specified otherwise. Write self-contained instructions, validate the schedule and show upcoming run times. Follow current access rules without increasing execution permissions, then open the created task.`,
};
content.create=user('create',assembled.create)+`<div class="sum" id="createSum">${I('bulb')}I’ll check the existing resources before configuring it.</div><div id="createSteps">`+
RESOURCES.map(([ic,title,desc],n)=>step('create',n,ic,title,`<span id="resource${n}">${desc}</span>`)).join('')+step('create',4,'shield','Create agent','Configuration approved')+`</div><div class="ans" id="createAns"></div>`;
const CREATE_ANSWER=`<p>Created <b>Reading Club Coordinator</b>.</p><p>It can use the event documents, read the event page through MCP and follow the installed brief-writing skill.</p>${nativeCard('agent','Reading Club Coordinator','Prepare sourced briefings, check arrangements and suggest next steps.',['Weekend reading club','deepseek-v4-flash','Event workspace','brief-writer'])}<p>You can open the agent to review or adjust its configuration.</p>`;

const agents=[
 {name:'Atlas',role:'Agenda',icon:'book',color:'#6d83a0',a:4.6,b:8.0,c:9.0,activity:'Reading the event guide',done:'Agenda checked',quote:'Nov 7, 14:00–16:00. Four reader talks, discussion and a shared reading list.'},
 {name:'Iris',role:'Sign-up',icon:'search',color:'#a88952',a:5.0,b:9.0,c:10.0,activity:'Checking sign-up rules',done:'Participation rules checked',quote:'20 free places. Register by Nov 5, 18:00. Bring a book and a short recommendation.'},
 {name:'Nova',role:'Preparation',icon:'tool',color:'#8c799e',a:5.4,b:10.0,c:10.8,activity:'Reviewing the preparation plan',done:'Open preparation tasks checked',quote:'Check registration on Nov 5, then the room, timer and reading-list template on Nov 6.'},
];
const BRIEF=`<h3>Weekend reading club — briefing</h3><p><b>Saturday, Nov 7 · 14:00–16:00 · Online</b></p><p>Bring a book and a two-minute recommendation. Registration closes <b>Nov 5 at 18:00</b>; there are 20 free places.</p><h3>Before the event</h3><p><b>Nov 5:</b> confirm registrations and remaining places.<br><b>Nov 6:</b> check the meeting room, timer and reading-list template.</p><p>The event guide and sign-up notes are ready. These two preparation checks are next.</p><p>${cite('Event guide')}${cite('Sign-up notes')}${cite('Preparation plan')}</p>`;
content.collaborate=user('collaborate',interactions[2].prompt)+`<div class="sum" id="collaborateSum">${I('bulb')}I’ll split the checks and combine the findings.</div><div id="team">`+agents.map((a,n)=>`<div class="child-task" id="agent${n}" style="--agent:${a.color}">${I(a.icon)}<div><div class="child-head"><span class="name">${a.name} <span style="color:#888;font-weight:400">· ${a.role}</span></span><span class="state" id="agent${n}State"></span></div><div class="activity" id="agent${n}Activity"></div><div class="track"><i id="agent${n}Progress"></i></div><div class="quote" id="agent${n}Quote">${a.quote}</div></div></div>`).join('')+`</div><div id="teamCompact"></div><div class="ans" id="collaborateAns"></div>`;

const AUTO_CARD=`<div class="native-card" id="automationCard"><div class="card-title">${I('automation')}Reading club briefing</div><div class="card-meta">Every weekday at 09:00 · Asia/Shanghai</div><div class="day-row">${['M','T','W','T','F','S','S'].map((v,n)=>`<span class="${n<5?'on':''}">${v}</span>`).join('')}</div><div class="card-meta">New conversation · Ask first<br>Notify on completion and when approval is needed</div><div class="inline-actions"><span class="btn" id="runNow">${I('play')}Run now</span><span style="font-size:17px;color:#8b8b8b">${I('edit')} Edit in Automations</span></div></div>`;
content.automate=user('automate',assembled.automate)+`<div class="sum" id="automateSum">${I('bulb')}I’ll use your event plan and local time zone.</div><div id="automateSteps">`+step('automate',0,'search','Find operations','Create automation')+step('automate',1,'kb','Check knowledge base','Weekend reading club · Preparation plan')+step('automate',2,'shield','Create automation','Schedule reviewed and approved')+`</div><div id="automationSaved"><div class="ans"><p>Created <b>Reading club briefing</b>.</p></div>${AUTO_CARD}</div><div id="automationRun"><div class="sum">${I('automation')}Reading club briefing · Running now</div><div class="work-chip" id="automationStatus"><span class="running-dot"></span>Checking the preparation plan</div><div class="ans" id="automateAns"></div></div>`;
const AUTOMATION_RESULT=`<h3>Your preparation brief</h3><p>The event guide and sign-up notes are ready.</p><ul><li><b>Nov 5:</b> check registrations and remaining places.</li><li><b>Nov 6:</b> test the room, timer and reading-list template.</li></ul><p>The briefing will run again on the next weekday at 09:00.</p>${cite('Preparation plan')}`;

content.approve=user('approve',interactions[4].prompt)+`<div class="sum" id="approveSum">${I('bulb')}I found the knowledge base. Here is the proposed change.</div><div id="approveSteps">`+step('approve',0,'kb','Find knowledge base','Weekend reading club · 3 documents')+step('approve',1,'shield','Update knowledge base','Name change · Awaiting review')+step('approve',2,'check','Update completed')+`</div><div class="ans" id="approveAns"></div>`;
const APPROVED_RESULT=`<p>Renamed the knowledge base to <b>Reading club workspace</b>.</p>${nativeCard('kb','Reading club workspace','Event guide, sign-up notes and the preparation plan.',['3 documents'])}`;

content.background=user('background',interactions[5].prompt)+`<div class="sum" id="backgroundSum">${I('bulb')}I’ll read the documents and build the run sheet.</div><div id="backgroundSteps">`+step('background',0,'skill','Read Skill','brief-writer · Markdown run sheet')+step('background',1,'term','Run sandbox command','Build the document in the background')+`</div><div class="work-chip" id="backgroundStatus"><span class="running-dot"></span>1 background task · Building run sheet</div><div class="shell" id="shell"><div class="command">$ python build_run_sheet.py</div><div class="meta" id="shellMeta">/workspace · Running in background</div><div class="out" id="shellOut"></div></div><div class="ans" id="backgroundAns"></div><div id="artifact"><div class="file-result" id="openArtifact">${I('file')}<div><b>reading-club-run-sheet.md</b><small>Markdown · Open preview</small></div>${I('chev','i chev')}</div><div class="preview-paper" id="artifactPreview"><h3>Reading club — run sheet</h3><p><b>Nov 7 · Online · 14:00–16:00</b></p><p>14:00 — Welcome and discussion rules<br>14:10 — Four reader talks<br>14:50 — Group discussion<br>15:30 — Build the shared reading list</p></div></div>`;

const extras={
 create:approval('create','Create “Reading Club Coordinator”',[
 ['Knowledge base','Weekend reading club'],['Model','deepseek-v4-flash'],['MCP services','Event workspace'],['Skills','brief-writer']]),
 automate:approval('automate','Create “Reading club briefing”',[
 ['Schedule','Weekdays at 09:00'],['Time zone','Asia/Shanghai'],['Conversation','New conversation · Ask first'],['Notifications','Completion and approval requests']]),
 approve:approval('approve','Rename knowledge base',[
 ['From','Weekend reading club'],['To','Reading club workspace']])+`<div class="access-menu" id="accessMenu"><div class="label">Execution permissions</div>${[['eye','Read only','Read and analyze without changing data.'],['shield','Ask first','Ask before operations that change data.'],['edit','Auto','Allow ordinary changes; sensitive actions may ask.']].map(([i,n,d],k)=>`<div class="access-option ${k===1?'selected':''}" id="accessOption${k}">${I(i)}<div><b>${n}</b><small>${d}</small></div>${k===1?I('check','i checkmark'):''}</div>`).join('')}</div>`,
};
const CONTEXT={intro:'',create:'Agents',collaborate:'Weekend reading club',automate:'Automations',approve:'Weekend reading club',background:'Reading club workspace'};
const productive=scenes.filter(s=>s.key!=='close'&&!s.wide);
$('chapters').innerHTML=productive.map((s,n)=>`<section class="chapter" id="s-${s.key}"><div class="editorial" id="${s.key}Editorial"><div class="eyebrow">${s.label}</div><h1>${s.title}</h1><p class="lede">${s.lede}</p><div class="explain" id="${s.key}Explain"></div></div><div class="chapter-foot"><img src="assets/logo-light.png"><span>v0.9.0</span></div><div class="assistant" id="${s.key}Panel"><header class="assistant-header">${I('assistant')}<span>WeKnora Assistant</span>${I('help','i help')}<span class="assistant-actions">${I('chat')}${I('hist')}${I('pin')}${I('close')}</span></header><div class="transcript" id="${s.key}Transcript"><div class="inner" id="${s.key}Inner">${content[s.key]}</div></div>${s.key==='intro'?welcome(s.key):''}${CONTEXT[s.key]?`<div class="context-chip">${I('panel')}Current page · ${CONTEXT[s.key]}</div>`:''}${composer(s.key)}${extras[s.key]||''}<div class="pointer" id="${s.key}Pointer">${cursorSVG}</div><div class="ripple" id="${s.key}Ripple"></div></div></section>`).join('');

// The same navigation proportions as the original film, with the new v0.9.0 entry.
const opening=document.createElement('div');opening.id='openingApp';opening.className='opening-app';
opening.innerHTML=`<div class="side"><div class="lg"><img src="assets/logo-light.png"><span><span class="entry" id="assistantEntry">${I('assistant')}</span>${I('panel')}</span></div>${[['chat','New Chat'],['kb','Knowledge Base'],['art','Artifacts'],['agent','Agents'],['automation','Automations'],['box','Toolbox'],['share','Shared Spaces']].map(([ic,n])=>`<div class="nav ${n==='Knowledge Base'?'on':''}">${I(ic)}${n}</div>`).join('')}<div class="sec">Conversations</div><div class="hist">No conversations yet</div></div><div class="main"><h2>Knowledge Base</h2><div class="desc">Your documents, organized for answers and actions.</div><div class="page-tabs"><span>All</span><span>Documents</span><span>Wiki</span><span>FAQ</span></div><div class="kb-grid">${nativeCard('kb','Weekend reading club','Event guide, sign-up and preparation.',['3 documents'])}${nativeCard('kb','Product guides','Release notes and setup guides.',['Documents'])}${nativeCard('kb','Team handbook','Shared ways of working.',['Wiki'])}</div></div><div class="entry-ring" id="entryRing"></div><div class="pointer" id="entryPointer">${cursorSVG}</div><div class="ripple" id="entryRipple"></div>`;
$('s-intro').insertBefore(opening,$('introPanel'));

// Reproduce the actual page entry and AssistantCreateDialog, then hand the
// expanded request to the sidebar. The dialog itself remains product UI.
for(const key of ['create','automate']){
 const isAgent=key==='create',label=isAgent?'Agents':'Automations';
 const page=document.createElement('div');page.className='creation-page';page.id=key+'Page';
 page.innerHTML=`<div class="compact-nav"><img src="assets/logo-light.png">${['panel','search','assistant','chat','kb','art','agent','automation','box'].map(ic=>`<span class="${ic===(isAgent?'agent':'automation')?'active':''}">${I(ic)}</span>`).join('')}</div><div class="creation-main"><div class="creation-page-heading"><h2>${I(isAgent?'agent':'automation')}${label}</h2><span class="entry-action" id="${key}Entry">${I(isAgent?'assistant':'plus')}${isAgent?'Create with assistant':'New automation'}</span></div><p>${isAgent?'Build agents for your knowledge and workflows.':'Let the WeKnora Assistant run scheduled tasks as you.'}</p>${isAgent?`<div class="creation-tabs">All <span>My agents</span><span>Shared spaces</span></div><div class="creation-grid">${nativeCard('chat','Quick Answer','Find answers in your knowledge bases.',['Built-in'])}${nativeCard('agent','Smart Reasoning','Work through complex tasks with tools.',['Built-in'])}${nativeCard('book','Reading Assistant','Find books, compare ideas and build a reading list.',['My agent'])}</div>`:`<div class="new-automation-card">${I('plus')}<b>New automation</b><span>You can also ask the WeKnora Assistant to create it</span></div>`}</div><div class="modal-dim" id="${key}Dim"></div><div class="create-dialog" id="${key}Dialog"><header>${I('assistant')}<b>${isAgent?'Describe your agent':'Create a task with the assistant'}</b>${I('close')}</header><div class="dialog-body"><p>${isAgent?'Describe who it serves and what it should do. The assistant will choose resources and create it.':'Describe what to do and when to run it. The assistant will turn it into an automation.'}</p><label>Task description</label><div class="dialog-textarea" id="${key}DialogInput" data-typewriter="${key}" data-chars="0"></div></div><footer><span class="btn">Configure manually</span><span class="btn primary" id="${key}Continue">${I('assistant')}Continue with assistant</span></footer></div><div class="pointer" id="${key}EntryPointer">${cursorSVG}</div><div class="ripple" id="${key}EntryRipple"></div></div>`;
 $('s-'+key).insertBefore(page,$(`${key}Panel`));
 const cover=document.createElement('div');cover.className='handoff-cover';cover.id=key+'HandoffCover';
 $('s-'+key).insertBefore(cover,$(`${key}Panel`));
}
// Keep the actual resource visible while approval changes it.
const renameList=document.createElement('div');renameList.id='renameList';renameList.className='rename-list';
renameList.innerHTML=`<header>${I('kb')}Knowledge Base<span id="renameSaveState"></span></header><div class="rename-tabs"><b>All</b><span>My knowledge bases</span><span>Shared spaces</span>${I('search')}</div><div class="rename-cards"><div class="rename-target" id="renameTarget"><div class="kb-card-icon">${I('kb')}<span>···</span></div><div class="rename-name"><b id="kbOldName">Weekend reading club</b><b id="kbNewName">Reading club workspace</b></div><p>Event guide, sign-up notes<br>and preparation plans.</p><footer>${I('file')}3 documents<span>Document</span></footer></div><div class="rename-secondary"><div class="kb-card-icon">${I('kb')}<span>···</span></div><b>Product guides</b><p>Release notes and setup<br>guides for the team.</p><footer>${I('file')}Documents</footer></div></div><div class="rename-confirm" id="renameConfirm">${I('check')}Knowledge base updated</div>`;
$('s-approve').appendChild(renameList);
mountExtraScenes({I,cursorSVG,interactions});

function reveal(el,s,a,d=.4){const p=P(s,a,d);el.style.display=p<=0?'none':'';show(el,p,10);}
function revealStep(key,n,s,a,done){const el=$(`${key}Step${n}`);reveal(el,s,a,.45);el.classList.toggle('run',s>=a&&s<done);}
function input(key,s,end){
 const i=interactions.find(x=>x.scene===key),sent=s>=i.send;
 if(key==='create'||key==='automate'){
  const handoff=s>=4.95&&s<5.65;
  setHTML($(`${key}In`),handoff?esc(i.prompt):'Ask a question or describe a task…');
  $(`${key}In`).className='ph'+(handoff?' typing':'');
  $(`${key}Send`).className=handoff?'on':'';
  setHTML($(`${key}Send`),s>=5.65&&s<end?I('stop'):I('up'));return;
 }
 const count=sent?0:i.chars.filter(c=>s+1e-7>=c.at).length;const value=i.prompt.slice(0,count);
 const el=$(`${key}In`);el.className='ph'+(count?' typing':'');el.dataset.chars=count;
 const caret=count&&!sent&&(s<i.end||Math.floor(s*2.5)%2===0)?'<span class="caret"></span>':'';
 setHTML(el,value?esc(value)+caret:'Ask a question or describe a task…');
 $(`${key}Send`).className=count?'on':'';
 setHTML($(`${key}Send`),sent&&s<end?I('stop'):I('up'));
 $(`${key}InputBox`).style.borderColor=count?'#c0cbc4':'';
}
function pointer(key,target,s,at,root=$(`${key}Panel`)){
 const cur=$(`${key}Pointer`),rip=$(`${key}Ripple`),visible=s>=at-.65&&s<at+.48;
 if(!visible){cur.style.opacity=0;rip.style.opacity=0;return}
 const box=target.getBoundingClientRect(),rr=root.getBoundingClientRect(),scale=rr.width/root.offsetWidth;
 const x=(box.left-rr.left+box.width*.62)/scale,y=(box.top-rr.top+box.height*.6)/scale,p=PI(s,at-.65,.65);
 cur.style.left=(x+140*(1-p))+'px';cur.style.top=(y+65*(1-p))+'px';cur.style.opacity=1-P(s,at+.25,.23);
 const q=lin(s,at,at+.48);rip.style.left=x+'px';rip.style.top=y+'px';rip.style.opacity=q>0&&q<1?.7*(1-q):0;rip.style.transform=`scale(${.35+1.1*eo(q)})`;
}
function dock(key,s,start,end){
 const el=$(`${key}Approval`),on=s>=start&&s<end;el.style.display=on?'block':'none';
 const p=P(s,start,.45);show(el,p,24);
 $(`${key}Transcript`).style.bottom=on?(190+(el.offsetHeight+14)*p)+'px':'191px';
}
function explanation(key,title,body,note='',identity=false){
 const visuals={create:[['edit','Describe'],['plug','Match resources'],['agent','Create']],collaborate:[['book','Atlas'],['search','Iris'],['tool','Nova']],automate:[['edit','Describe'],['clock','Schedule'],['automation','Run']]};
 setHTML($(`${key}Explain`),`<h2>${title}</h2><p>${body}</p>${visuals[key]?`<div class="visual-flow">${visuals[key].map(([ic,text])=>`<span>${I(ic)}<b>${text}</b></span>`).join('<i>→</i>')}</div>`:''}${note&&!visuals[key]?`<div class="note">${note}</div>`:''}${identity?`<div class="identity">${I('shield')}Acts as you, with your permissions.</div>`:''}`);
}

function creationFlow(key,s){
 const i=interactions.find(x=>x.scene===key),page=$(`${key}Page`),modal=$(`${key}Dialog`),p=PI(s,4.85,.5),opening=P(s,1.15,.35),closing=P(s,4.75,.2);
 page.style.opacity=1;page.style.visibility=s<5.35?'visible':'hidden';
 $(`${key}Dim`).style.opacity=opening*(1-closing);
 modal.style.opacity=opening*(1-closing);modal.style.transform=`translate(-50%, -50%) scale(${1-.035*closing})`;
 const count=s>=i.send?0:i.chars.filter(c=>s+1e-7>=c.at).length,el=$(`${key}DialogInput`);
 el.dataset.chars=count;setHTML(el,count?esc(i.prompt.slice(0,count))+'<span class="caret"></span>':'Describe the task…');el.classList.toggle('filled',count>0);
 $(`${key}Continue`).classList.toggle('disabled',count===0);
 const panel=$(`${key}Panel`);panel.style.opacity=1;panel.style.visibility=p>0?'visible':'hidden';panel.dataset.entering=String(p<1);panel.style.transform=`translateX(${1000*(1-p)}px)`;
 $(`${key}HandoffCover`).style.transform=`translateX(${-950*(1-p)}px)`;
 $(`${key}Editorial`).style.opacity=P(s,5.2,.35);$('s-'+key).querySelector('.chapter-foot').style.opacity=P(s,5.2,.35);
 pointer(key+'Entry',s<2?$(`${key}Entry`):$(`${key}Continue`),s,s<2?1.15:4.75,page);
}
// Reuse the original film's smooth transcript-following behavior. A direct seek
// resets the follow position; sequential export follows at a damped 9 Hz rate.
let current=0,previous=-1;
function scroll(key){
 const col=$(`${key}Transcript`),inner=$(`${key}Inner`),target=Math.max(0,inner.scrollHeight-col.clientHeight),dt=current-previous;
 const y=dt>0&&dt<.1&&inner._y!=null?inner._y+(target-inner._y)*(1-Math.exp(-dt*9)):target;
 inner._y=y;inner.style.transform=`translateY(${-y}px)`;
}

function renderIntro(s){
 const transition=PI(s,4.6,.85),open=P(s,3.65,.8),brand=1-P(s,2.45,.5);
 $('opening').style.opacity=brand;$('opening').style.display=brand>0?'block':'none';
 $('opening').firstElementChild.style.transform=`scale(${1+.025*lin(s,0,2.7)})`;
 $('openingApp').style.opacity=1-transition;$('openingApp').style.transform=`translateX(${-35*transition}px)`;
 $('introPanel').style.opacity=open;$('introPanel').style.transform=`translateX(${(1-open)*120}px)`;
 $('introEditorial').style.opacity=transition;$('introEditorial').style.transform=`translateX(${-25*(1-transition)}px)`;
 $('s-intro').querySelector('.chapter-foot').style.opacity=transition;
 $('entryRing').style.opacity=P(s,2.8,.35)*(1-P(s,4.05,.3));
 pointer('entry',$('assistantEntry'),s,3.65,$('openingApp'));
 $('introWelcome').style.display=s<8.15?'block':'none';
 reveal($('introU'),s,8.15);reveal($('introSum'),s,8.55);
 [8.9,10.0,10.9,11.8].forEach((a,n)=>revealStep('intro',n,s,a,a+.8));
 stream($('introAns'),VERSION_ANSWER,s,12.15,105,false);
 input('intro',s,20.6);
 pointer('intro',$('introSend'),s,8.15);
 if(s<8.9)explanation('intro','A new entry, right beside the logo.','Open the Assistant without leaving your current page.');
 else if(s<12.15)explanation('intro','Search first. Read the source.','The Assistant searches bundled product docs, then reads the release notes and the relevant guide.','PRODUCT DOCS → RELEASE NOTES → ASSISTANT GUIDE');
 else explanation('intro','The product, explained in context.','Get a sourced overview of v0.9.0—from Assistant and agent collaboration to automations, permissions and background tasks.','RELEASES · FEATURES · SETUP · TROUBLESHOOTING',true);
 scroll('intro');
}
function renderCreate(s){
 const entryTime=s;s-=1.7;
 reveal($('createU'),s,3.95);reveal($('createSum'),s,4.3);
 [4.7,5.8,6.9,8.0,15.4].forEach((a,n)=>revealStep('create',n,s,a,n===4?16:a+1));
 RESOURCES.forEach((_,n)=>{$(`resource${n}`).style.opacity=P(s,5.35+n*1.1,.35)});
 dock('create',s,10.1,15.4);
 stream($('createAns'),CREATE_ANSWER,s,16.05,100,false);
 input('create',entryTime,22.6);
 pointer('create',$('createApprove'),s,15.05);
 if(s<4.7)explanation('create','Describe it once. Carry the context.','The request arrives with resource checks and your current access rules. The Assistant takes it from here.');
 else if(s<10.1)explanation('create','Use resources you already have.','The Assistant checks available knowledge bases, models, MCP services and installed skills before putting them together.','KNOWLEDGE → MODEL → MCP → SKILL');
 else if(s<16.05)explanation('create','Review the assembled configuration.','See the selected resources before the agent is created. Ask first keeps the write operation pending until you approve.', '',true);
 else explanation('create','A configured agent, ready to use.','The reading-club coordinator has its knowledge and tools attached. Open the agent whenever you want to refine it.');
 scroll('create');
 creationFlow('create',entryTime);
}
function renderCollaborate(s){
 reveal($('collaborateU'),s,3.75);reveal($('collaborateSum'),s,4.1);
 const collapse=PI(s,10.95,.65);$('team').style.display='block';$('team').style.maxHeight='none';$('team').style.opacity=1-collapse;
 agents.forEach((a,n)=>{
  reveal($(`agent${n}`),s,a.a);const done=s>=a.b,synced=s>=a.c;
  $(`agent${n}`).classList.toggle('done',done);
  $(`agent${n}State`).textContent=synced?'Synced to main chat':done?'Completed':'Running';
  $(`agent${n}Activity`).textContent=done?a.done:a.activity;
  $(`agent${n}Progress`).style.transform=`scaleX(${lin(s,a.a+.3,a.b)})`;
  $(`agent${n}Quote`).style.display=done?'block':'none';
 });
 const teamHeight=$('team').scrollHeight;$('team').style.maxHeight=teamHeight*(1-collapse)+'px';$('team').style.overflow='hidden';
 setHTML($('teamCompact'),agents.map(a=>`<div class="task-compact" style="color:${a.color}">${I(a.icon)}${a.name} · ${a.role}<span>Completed · Synced ${I('check')}</span></div>`).join(''));
 $('teamCompact').style.display=collapse>0?'block':'none';$('teamCompact').style.opacity=collapse;
 stream($('collaborateAns'),BRIEF,s,11.55,92,false);
 input('collaborate',s,16.8);pointer('collaborate',$('collaborateSend'),s,3.75);
 if(s<5.4)explanation('collaborate','Split the work by responsibility.','Atlas checks the agenda. Iris checks sign-up rules. Nova reviews the preparation plan.');
 else if(s<11.55)explanation('collaborate','Watch each agent work.','Named tasks show what is running, what has finished and which findings have returned to the main conversation.','ATLAS · AGENDA    IRIS · SIGN-UP    NOVA · PREPARATION');
 else explanation('collaborate','An answer you can act on.','The combined briefing includes the event time, registration deadline and the next preparation tasks, with links to the source documents.');
 scroll('collaborate');
}
function renderAutomate(s){
 const entryTime=s;s-=2;
 reveal($('automateU'),s,3.65);reveal($('automateSum'),s,4.0);
 [4.35,5.25,10.5].forEach((a,n)=>revealStep('automate',n,s,a,a+.7));
 dock('automate',s,6.3,10.5);reveal($('automationSaved'),s,10.65);
 reveal($('automationRun'),s,13.0);
 setHTML($('automationStatus'),s<14.5?'<span class="running-dot"></span>Checking the preparation plan':I('check')+'Completed · Result ready');
 stream($('automateAns'),AUTOMATION_RESULT,s,14.5,96,false);
 input('automate',entryTime,20.0);
 if(s<11.5)pointer('automate',$('automateApprove'),s,10.15);
 else pointer('automate',$('runNow'),s,12.8);
 if(s<6.3)explanation('automate','The request carries the essentials.','Your task, local time zone and access rules reach the Assistant together. It checks the plan before scheduling.');
 else if(s<10.65)explanation('automate','Make the schedule explicit.','Weekdays at 09:00, in Asia/Shanghai. Review the conversation mode, permissions and notifications before saving.');
 else if(s<14.5)explanation('automate','Save it. Try a run.','Run the task now to verify the result. You can edit the instruction and schedule later in Automations.');
 else explanation('automate','A useful brief, every morning.','Results and approval requests reach you through notifications. Each run keeps its conversation and history.','WEEKDAYS · 09:00 · ASIA/SHANGHAI',true);
 scroll('automate');
 creationFlow('automate',entryTime);
}
function renderApprove(s){
 $('accessMenu').style.display=s>=.3&&s<1.75?'block':'none';show($('accessMenu'),P(s,.3,.35),14);
 reveal($('approveU'),s,4.85);reveal($('approveSum'),s,5.2);
 [5.5,6.15,10.0].forEach((a,n)=>revealStep('approve',n,s,a,n===1?9.6:a+.55));
 const status=$('approveStep1').querySelector('small');status.textContent=s<9.6?'Name change · Awaiting review':'Name change · Approved';
 dock('approve',s,6.25,9.95);
 stream($('approveAns'),APPROVED_RESULT,s,10.6,100,false);
 input('approve',s,12.8);
 if(s<1.9)pointer('approve',$('accessOption1'),s,1.45);
 else if(s<6.25)pointer('approve',$('approveSend'),s,4.85);
 else pointer('approve',$('approveApprove'),s,9.6);
 if(s<4.85)explanation('approve','Read only. Ask first. Auto.','Choose the execution level for the task. Auto can allow ordinary writes; sensitive operations can still require confirmation.');
 else if(s<9.95)explanation('approve','See exactly what will change.','The approval names the resource and shows the current and proposed values. The operation waits for your decision.','FROM: WEEKEND READING CLUB\nTO: READING CLUB WORKSPACE',true);
 else explanation('approve','Confirm, execute, verify.','The timeline records the approval and completed operation. The updated knowledge base is available immediately.', '',true);
 scroll('approve');
 const list=P(s,6.6,.65),change=P(s,10.15,.45),done=P(s,10.65,.35);
 $('renameList').style.opacity=list;$('renameList').style.transform=`translateY(${28*(1-list)}px)`;$('renameList').style.visibility=list>0?'visible':'hidden';
 $('approveExplain').style.display=s<6.6?'':'none';
 $('kbOldName').style.opacity=1-change;$('kbOldName').style.transform=`translateY(${-14*change}px)`;
 $('kbNewName').style.opacity=change;$('kbNewName').style.transform=`translateY(${14*(1-change)}px)`;
 $('renameTarget').style.borderColor=s>=9.95&&s<11.65?'#bd9b62':'#e1e5e2';
 $('renameTarget').style.boxShadow=`0 0 0 ${2*P(s,9.95,.25)*(1-P(s,11.35,.6))}px #b8863b18`;
 setHTML($('renameSaveState'),s<9.95?'Awaiting approval':s<10.65?'Updating name…':I('check')+'Saved');
 show($('renameConfirm'),done,9);
}

function renderBackground(s){
 reveal($('backgroundU'),s,3.15);reveal($('backgroundSum'),s,3.5);
 [3.85,4.6].forEach((a,n)=>revealStep('background',n,s,a,n===1?7.8:4.45));
 reveal($('backgroundStatus'),s,4.9);reveal($('shell'),s,5.2);
 setHTML($('backgroundStatus'),s<7.8?'<span class="running-dot"></span>1 background task · Building run sheet':I('check')+'Background task completed');
 $('shellMeta').textContent=s<7.8?'/workspace · Running in background':'/workspace · Exit code 0 · Completed';
 const lines=[['Read event guide',5.55],['Read sign-up notes',5.95],['Read preparation plan',6.35],['Build Markdown run sheet',6.9],['Saved reading-club-run-sheet.md',7.8]];
 $('shellOut').textContent=lines.filter(([,a])=>s>=a).map(([l])=>'✓ '+l).join('\n');
 stream($('backgroundAns'),'<p>Done. The run sheet includes the event agenda and preparation checks.</p>',s,8.05,95,false);
 reveal($('artifact'),s,8.7);reveal($('artifactPreview'),s,9.6);
 input('background',s,9.3);
 pointer('background',s<8.7?$('backgroundSend'):$('openArtifact'),s,s<8.7?3.15:9.45);
 if(s<4.9)explanation('background','From documents to a deliverable.','Use a skill and the sandbox to turn knowledge into a file you can keep.');
 else if(s<8.05)explanation('background','Follow the task without blocking the chat.','Background status, command output and completion remain attached to the conversation.');
 else explanation('background','Open the finished file.','The run sheet gives the host a clear sequence for the event. Preview the artifact and return to the conversation.','READING-CLUB-RUN-SHEET.MD');
 scroll('background');
}
const functions={intro:renderIntro,create:renderCreate,collaborate:renderCollaborate,automate:renderAutomate,approve:renderApprove,background:renderBackground};

export function seek(time){
 const t=cl(time,0,duration);current=t;
 document.documentElement.style.setProperty('--rot',`${(t*250)%360}deg`);
 if(t>=22)$('opening').style.display='none';
 for(const scene of scenes.filter(s=>s.key!=='close')){
  const root=$('s-'+scene.key),active=t>=scene.a&&t<scene.b;
  root.style.display=active?'block':'none';if(!active)continue;
  const s=t-scene.a,incoming=scene.a===0?1:lin(s,.08,.65),outgoing=lin(t,scene.b-.42,scene.b);
  root.style.opacity=Math.min(incoming,1-outgoing);
  root.style.transform=`translateX(${(1-eo(incoming))*140-eio(outgoing)*140}px)`;
  if(scene.wide){renderExtraScene(scene.key,s,{I,interactions});continue;}
  if(scene.key!=='intro'){
   const children=$(`${scene.key}Editorial`).children;
   [...children].forEach((el,n)=>show(el,P(s,.15+n*.08,.65),16));
   // Push toward the active task, then return before the chapter hand-off.
   const push=PI(s,4.2,1.15)*(1-PI(s,scene.b-scene.a-2,1.15));
   $(`${scene.key}Panel`).style.transform=`scale(${1+.025*push})`;
  }
  functions[scene.key](s);
  const phases={intro:[0,8.9,12.15],create:[0,6.4,11.8,17.75],collaborate:[0,5.4,11.55],automate:[0,8.3,12.65,16.5],approve:[0,4.85,9.95],background:[0,4.9,8.05]}[scene.key];
  const phase=phases.filter(a=>s>=a).at(-1),next=phases.find(a=>a>s)??1000;
  const ep=Math.min(P(s,phase+.02,.42),1-lin(s,next-.14,next));
  show($(`${scene.key}Explain`),ep,10);
 }
 const ending=t>=161; $('closing').style.display=ending?'block':'none';
 if(ending){const p=P(t,161,.65);$('closing').style.opacity=p;$('closing').firstElementChild.style.transform=`translateY(${(1-p)*14}px) scale(${.98+.02*p})`;}
 const caption=captions.find(([a,b])=>t>=a&&t<b);
 $('subtitle').textContent=caption?.[2]||'';$('subtitle').style.display=caption?'block':'none';
 $('progress').firstElementChild.style.transform=`scaleX(${t/duration})`;
 $('seconds').textContent=t.toFixed(1)+' s';$('time').value=t;previous=t;
}
await document.fonts.ready;
window.film={seek,duration,scenes,captions,interactions,keyEvents,ready:true};
const qs=new URLSearchParams(location.search);if(qs.has('render'))$('controls').style.display='none';
let play=false,started=0;
const resize=()=>{$('stage').style.transform=`scale(${Math.min(innerWidth/1920,innerHeight/1080)})`;};addEventListener('resize',resize);resize();
$('time').oninput=()=>{play=false;seek(Number($('time').value))};
$('play').onclick=()=>{play=!play;started=performance.now()/1000-current;};
$('replay').onclick=()=>{play=true;started=performance.now()/1000;};
function tick(){if(play){seek(Math.min(duration,performance.now()/1000-started));if(current>=duration)play=false}requestAnimationFrame(tick)}
seek(Number(qs.get('t')||0));requestAnimationFrame(tick);
