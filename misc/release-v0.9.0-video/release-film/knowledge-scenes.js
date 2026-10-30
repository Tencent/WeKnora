import {$,P,PI,lin,show,stream,setHTML,IC} from './foundation.js';

// These follow the current BrowserSkill, gallery, WikiBrowser and source-viewer
// components. All documents, drawings, forms and graph nodes are fictional.
export const extraScenes=[
 {a:113,b:127,key:'browser',wide:true,label:'07 — BROWSER SKILL',title:'Put the browser to work.',sub:'Fill the page from your knowledge. Keep the final review in your hands.'},
 {a:127,b:137,key:'gallery',wide:true,label:'08 — KNOWLEDGE GALLERY',title:'Find the picture in your knowledge.',sub:'Browse extracted images. Search their descriptions and text. Open the source.'},
 {a:137,b:150,key:'wiki',wide:true,label:'09 — WIKI GRAPH',title:'See how the ideas connect.',sub:'Explore topic groups, focus the graph and read the page without losing your place.'},
 {a:150,b:161,key:'citation',wide:true,label:'10 — SOURCE CITATIONS',title:'From an answer to its evidence.',sub:'Preview a citation, then open the original at the passage that supports it.'},
];
export const extraInputs=[
 {scene:'browser',start:.65,end:2.7,send:3.0,prompt:'Fill the event form from the reading-club plan. Leave it open for my review.'},
 {scene:'browser',id:'browser-title',start:5.0,end:5.95,send:6.15,retain:true,prompt:'Weekend reading club'},
 {scene:'browser',id:'browser-notes',start:6.55,end:8.65,send:8.9,retain:true,prompt:'Bring a book and a two-minute recommendation. Register by Nov 5 at 18:00.'},
 {scene:'gallery',start:1.05,end:2.35,send:2.65,retain:true,prompt:'reading club'},
 {scene:'citation',start:.7,end:2.25,send:2.55,prompt:'When does registration close, and what should I bring?'},
];
const esc=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;');
const art=(kind,large=false)=>{
 const W=480,H=kind==='poster'?560:kind==='cover'?520:kind==='chart'?320:360;
 const palettes={poster:['#f4e5cf','#203652','#b78547'],cover:['#24364c','#f5e9d6','#b59a6e'],chart:['#e8edf0','#405d72','#9baebb'],diagram:['#f0ece1','#797261','#c6b58c'],note:['#eee4e2','#825b57','#bd9691'],agenda:['#ecede7','#536257','#93a189']};
 const [bg,fg,acc]=palettes[kind]||palettes.agenda;
 let content='';
 if(kind==='poster')content=`<text x="42" y="64" font-family="Inter" font-size="17" letter-spacing="4">WEEKEND / VOL. 07</text><text x="40" y="140" font-family="Georgia" font-size="59">Reading</text><text x="40" y="198" font-family="Georgia" font-size="59">together.</text><path d="M100 285 Q180 245 240 286 Q300 245 380 285 L380 413 Q300 376 240 420 Q180 376 100 413Z" fill="none" stroke="${fg}" stroke-width="3"/><path d="M240 286V420M121 303Q180 276 217 301M121 325Q180 298 217 323M264 302Q320 278 359 301M264 324Q320 300 359 323" fill="none" stroke="${fg}" stroke-width="2"/><circle cx="378" cy="203" r="25" fill="${acc}"/><path d="M40 460H440" stroke="${fg}" opacity=".3"/><text x="40" y="502" font-family="Inter" font-size="19">NOV 7 · 14:00–16:00 · ONLINE</text><text x="40" y="535" font-family="Inter" font-size="16">Bring a book. Share a new perspective.</text>`;
 else if(kind==='cover')content=`<path d="M40 45H440V475H40Z" stroke="${acc}" fill="none"/><text x="73" y="110" font-family="Inter" font-size="17" letter-spacing="4">THE READING CLUB</text><text x="73" y="210" font-family="Georgia" font-size="52">A shared</text><text x="73" y="269" font-family="Georgia" font-size="52">reading list.</text><path d="M73 329H407M73 344H311M73 359H373" stroke="${acc}" stroke-width="3"/><text x="73" y="430" font-family="Inter" font-size="18">Ideas to take into the next week.</text>`;
 else if(kind==='chart')content=`<text x="36" y="51" font-family="Inter" font-size="22">Reader interests</text>${[.56,.84,.68,.39].map((v,i)=>`<rect x="${49+i*103}" y="${253-v*175}" width="60" height="${v*175}" rx="3" fill="${i===1?fg:acc}"/><text x="${79+i*103}" y="282" text-anchor="middle" font-family="Inter" font-size="14">${['Fiction','Ideas','History','Poetry'][i]}</text>`).join('')}`;
 else if(kind==='diagram')content=`<text x="36" y="52" font-family="Inter" font-size="22">From book to discussion</text>${['READ','SHARE','CONNECT'].map((t,i)=>`<circle cx="${88+i*151}" cy="177" r="45" fill="none" stroke="${acc}" stroke-width="2"/><text x="${88+i*151}" y="182" text-anchor="middle" font-family="Inter" font-size="14">${t}</text>${i<2?`<path d="M${139+i*151} 177h48m-7-5 7 5-7 5" fill="none" stroke="${fg}"/>`:''}`).join('')}<text x="240" y="291" text-anchor="middle" font-family="Georgia" font-size="22">A conversation starts with a page.</text>`;
 else if(kind==='note')content=`<text x="36" y="56" font-family="Georgia" font-size="34">Bring your perspective.</text><text x="36" y="113" font-family="Inter" font-size="19">01  Pick a passage</text><text x="36" y="163" font-family="Inter" font-size="19">02  Ask a question</text><text x="36" y="213" font-family="Inter" font-size="19">03  Listen for a new idea</text><path d="M36 265H442" stroke="${acc}"/><text x="36" y="313" font-family="Inter" font-size="17">READING CLUB · DISCUSSION NOTES</text>`;
 else content=`<text x="36" y="54" font-family="Inter" font-size="23">The afternoon at a glance</text>${[['14:00','Welcome'],['14:10','Reader talks'],['14:50','Open discussion'],['15:30','Shared reading list']].map(([t,n],i)=>`<text x="36" y="${110+i*55}" font-family="Inter" font-size="17">${t}</text><text x="145" y="${110+i*55}" font-family="Georgia" font-size="24">${n}</text><path d="M36 ${127+i*55}H440" stroke="${acc}" opacity=".5"/>`).join('')}`;
 return `<svg class="knowledge-art ${large?'large':''}" viewBox="0 0 ${W} ${H}" xmlns="http://www.w3.org/2000/svg" style="color:${fg}"><rect width="${W}" height="${H}" fill="${bg}"/><g fill="${fg}">${content}</g></svg>`;
};
const galleryItems=[['poster','Reading club poster','Event guide.pdf'],['chart','Reader interests','Event guide.pdf'],['cover','Shared reading list','Event guide.pdf'],['agenda','Event agenda','Event guide.pdf'],['diagram','Discussion guide','Preparation plan.pdf'],['note','Discussion notes','Preparation plan.pdf'],['diagram','Ask a good question','Preparation plan.pdf'],['agenda','Preparation checklist','Preparation plan.pdf'],['chart','Previous sessions','Event guide.pdf'],['note','Notes from the host','Preparation plan.pdf'],['cover','Books to revisit','Event guide.pdf'],['poster','Next month’s session','Event guide.pdf']];
const graphGroups=[
 {name:'Reading club',x:340,y:310,rx:265,ry:224,color:'#a28b58',nodes:[[-103,-118,'Event guide','summary'],[112,-99,'Registration','concept'],[-23,-29,'Reading club','entity'],[-142,66,'Agenda','synthesis'],[112,88,'Preparation','concept'],[-6,155,'Reading list','summary']]},
 {name:'Ways of reading',x:898,y:220,rx:226,ry:168,color:'#6b8e9e',nodes:[[-101,-69,'Reading notes','summary'],[58,-84,'Close reading','concept'],[-6,12,'Discussion','entity'],[118,61,'Compare ideas','comparison'],[-116,86,'Good questions','concept']]},
 {name:'Community',x:1130,y:498,rx:223,ry:155,color:'#8c799e',nodes:[[-92,-48,'Hosting guide','summary'],[98,-71,'Facilitator','entity'],[2,21,'Participation','concept'],[-108,79,'Shared notes','synthesis'],[128,81,'New members','entity']]},
];
const colors={summary:'#0052d9',entity:'#2ba471',concept:'#e37318',synthesis:'#0594fa',comparison:'#d54941'};
let I,cursor;
function nav(active){return `<div class="compact-nav">${['panel','search','assistant','chat','kb','art','agent','automation','box'].map(ic=>`<span class="${ic===active?'active':''}">${I(ic)}</span>`).join('')}</div>`;}
function kbHeader(tab){return `<div class="knowledge-head"><b>Knowledge Base <span>›</span> Weekend reading club</b><div class="knowledge-tabs">${[['file','Documents'],['book','Wiki'],['graph','Graph'],['image','Gallery']].map(([ic,t])=>`<span class="${tab===t?'active':''}">${I(ic)}${t}</span>`).join('')}</div>${I('help')}</div>`;}
function mouse(key){return `<div class="pointer" id="${key}Cursor">${cursor}</div><div class="ripple" id="${key}Click"></div>`;}
function graph(){return `<svg viewBox="0 0 1500 650" class="graph-drawing" id="graphDrawing">${graphGroups.map((g,k)=>`<g id="graphGroup${k}" style="transform-origin:${g.x}px ${g.y}px"><ellipse cx="${g.x}" cy="${g.y}" rx="${g.rx}" ry="${g.ry}" fill="#f6f7f8" stroke="#eef0f2"/><g stroke="#dce1e8" stroke-width="1.2">${g.nodes.map((n,i)=>i?`<path d="M${g.x+n[0]} ${g.y+n[1]}L${g.x+g.nodes[2][0]} ${g.y+g.nodes[2][1]}"/>`:'').join('')}${g.nodes.slice(1).map((n,i)=>`<path d="M${g.x+n[0]} ${g.y+n[1]}L${g.x+g.nodes[i][0]} ${g.y+g.nodes[i][1]}"/>`).join('')}</g><g>${g.nodes.map(([x,y,t,type],n)=>`<g id="wikiNode${k}-${n}" class="wiki-node"><circle cx="${g.x+x}" cy="${g.y+y}" r="${n===2?9:6}" fill="${colors[type]}"/><text x="${g.x+x}" y="${g.y+y+28}" text-anchor="middle" fill="${n===2?'#313b43':'#77818a'}" font-family="Inter" font-size="${n===2?22:19}" font-weight="${n===2?550:400}">${t}</text></g>`).join('')}</g><g id="wikiGroupLabel${k}"><rect x="${g.x-104}" y="${g.y-g.ry-32}" width="208" height="39" rx="19" fill="white" stroke="#e0e4e7"/><text x="${g.x}" y="${g.y-g.ry-7}" text-anchor="middle" font-family="Inter" font-size="17" fill="#536374">${g.name} ›</text></g></g>`).join('')}</svg>`;}

export function mountExtraScenes(helpers){
 I=helpers.I;cursor=helpers.cursorSVG;
 IC.expand='<path d="M9 3H3v6M15 3h6v6M3 15v6h6M21 15v6h-6"/>';
 IC.image='<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8" cy="8" r="1.5"/><path d="m3 17 5-5 4 4 4-6 5 7"/>';
 const html={};
 html.browser=`<div class="browser-window" id="browserWindow"><div class="browser-tabs"><i></i><i></i><i></i><span>${I('web')}Event draft <b>×</b></span></div><div class="browser-address">←　→　${I('sync')}<span>${I('lock')}events.example/reading-club/edit</span>${I('plus')}</div><div class="browser-control-banner">${I('web')}BrowserSkill is controlling this browser <span>Cancel</span></div><div class="event-form"><div class="event-brand">FIELDNOTES <span>EVENTS / DRAFT</span></div><h2>A good afternoon,<br>planned together.</h2><div class="event-fields"><label>Event title</label><div class="form-field" id="browser-titleIn" data-typewriter="browser-title" data-chars="0"></div><div class="form-row"><div><label>Date &amp; time</label><div class="form-field" id="eventDate">Choose a date</div></div><div><label>Capacity</label><div class="form-field" id="eventCapacity">Add capacity</div></div></div><label>What guests should know</label><div class="form-field multiline" id="browser-notesIn" data-typewriter="browser-notes" data-chars="0"></div><div class="event-bottom"><span id="eventDraftState">Your changes are a draft.</span><b>Publish event</b></div></div></div>${mouse('browserPage')}</div><div class="browser-task" id="browserTask"><header>${I('agent')}Smart Reasoning<span>${I('hist')}${I('close')}</span></header><div class="browser-chat"><div class="umsg" id="browserUser"><div>${extraInputs[0].prompt}</div></div><div id="browserSteps"><div class="step" id="browserStep0"><span class="si">${I('skill')}</span><div class="st">Read Browser Skill<small>Use the connected local browser</small></div></div><div class="step" id="browserStep1"><span class="si">${I('web')}</span><div class="st">Open event draft<small>Independent browser task window</small></div></div><div class="step" id="browserStep2"><span class="si">${I('edit')}</span><div class="st" id="browserOperation">Fill from the event guide<small>Title, schedule and preparation notes</small></div></div></div><div class="ans" id="browserAnswer"></div></div><div class="task-input"><div id="browserIn" data-typewriter="browser" data-chars="0"></div><footer><span>${I('web')}Local browser <i class="toggle-on"></i></span><b id="browserSend">${I('up')}</b></footer></div></div><aside class="browser-live" id="browserLive"><header>${I('web')}<b>events.example</b><span>${I('expand')}</span></header><div class="live-preview" id="browserMirror"></div><footer><span class="live-status"><i></i><span id="browserState">Running</span></span><span class="preview-control" id="browserPause">${I('pause')}Pause</span><span class="preview-control">${I('stop')}End</span></footer></aside>${mouse('browser')}`;
 html.gallery=`<div class="wide-app" id="galleryApp">${nav('kb')}${kbHeader('Gallery')}<div class="gallery-content"><div class="gallery-tools"><b>All images <small id="galleryCount">12 images</small></b><div class="gallery-search">${I('search')}<span id="galleryIn" data-typewriter="gallery" data-chars="0"></span></div><span>${I('file')}Source</span><span>${I('rank')}Sort</span><span>${I('tag')}Filter</span></div><div class="masonry" id="galleryMasonry">${galleryItems.map(([kind,name,source],n)=>`<div class="gallery-tile" id="galleryTile${n}">${art(kind)}<div><b>${name}</b><span>${I('file')}${source}</span></div></div>`).join('')}</div></div><div class="gallery-viewer" id="galleryViewer"><div class="image-stage"><header><span>1 / 4　Event guide.pdf</span><span>−　100%　+　1:1　↻　${I('image')}　${I('close')}</span></header><div class="large-art" id="galleryLargeArt">${art('poster',true)}</div><div class="image-thumbs">${['poster','agenda','cover','diagram'].map((k,n)=>`<span class="${n===0?'active':''}">${art(k)}</span>`).join('')}</div></div><aside class="image-info"><h3>Image description</h3><p>A reading-club event poster. The open-book illustration introduces an online afternoon of reader talks and discussion.</p><h3>Text in image</h3><p>WEEKEND / VOL. 07<br>Reading together.<br>NOV 7 · 14:00–16:00 · ONLINE<br>Bring a book. Share a new perspective.</p><div class="image-basic"><h3>Basic information</h3><dl><dt>Source document</dt><dd>${I('file')}Event guide.pdf</dd><dt>Dimensions</dt><dd>480 × 560</dd><dt>Status</dt><dd>Enabled</dd></dl></div></aside></div>${mouse('gallery')}</div>`;
 html.wiki=`<div class="wide-app" id="wikiApp">${nav('kb')}${kbHeader('Graph')}<div class="wiki-canvas"><div class="wiki-search">${I('search')}Search Wiki pages… ${I('chevd')}</div>${graph()}<div class="wiki-legend">${Object.entries(colors).map(([k,c])=>`<span><i style="background:${c}"></i>${k[0].toUpperCase()+k.slice(1)}</span>`).join('')}<hr><span>${I('graph')}Fit view</span><span>${I('eye')}Hide unlinked</span><hr><small id="wikiNodeCount">16 / 16 nodes</small></div><div class="wiki-focus" id="wikiFocus">Reading club ${I('chevd')}<span>Back to full graph　Esc</span></div><div class="graph-hint">Drag to pan · Scroll to zoom</div></div><div class="wiki-reader" id="wikiReader"><header><div><b>Reading club</b><span>Entity <i>v3</i></span></div>${I('close')}</header><div class="wiki-markdown"><h1>Weekend reading club</h1><p>A shared afternoon to discover books, compare ideas and build a reading list together.</p><blockquote>Bring one book and a two-minute recommendation.</blockquote><h2>At a glance</h2><table><tbody><tr><th>When</th><td>Nov 7, 14:00–16:00</td></tr><tr><th>Where</th><td>Online</td></tr><tr><th>Places</th><td>20 · Free registration</td></tr></tbody></table><h2>How the afternoon works</h2><ol><li>Four short reader talks</li><li>Open discussion and questions</li><li>A shared reading list to take away</li></ol><h2>Related knowledge</h2><p><a>Event guide</a><a>Registration</a><a>Preparation</a></p></div></div>${mouse('wiki')}</div>`;
 html.citation=`<div class="wide-app" id="citationApp">${nav('chat')}<div class="citation-top">Reading club questions<span>${I('share')}${I('hist')}</span></div><div class="citation-conversation" id="citationConversation"><div class="citation-messages"><div class="umsg" id="citationUser"><div>${extraInputs.at(-1).prompt}</div></div><div class="step" id="citationSearch"><div class="si">${I('search')}</div><div class="st">Search knowledge<small>Weekend reading club · Sign-up notes</small></div></div><div class="ans" id="citationAnswer"></div><div class="citation-pop" id="citationPop"><header>${I('book')}Sign-up notes.pdf</header><p>Registration closes on November 5 at 18:00. Each participant should bring a book and prepare a two-minute recommendation.</p><footer>Page 1 · Open source ${I('chev')}</footer></div></div><div class="task-input"><div id="citationIn" data-typewriter="citation" data-chars="0"></div><footer><span>${I('kb')}Weekend reading club</span><b id="citationSend">${I('up')}</b></footer></div></div><div class="source-viewer" id="sourceViewer"><header>${I('file')}<div><b>Sign-up notes.pdf</b><small id="sourceLocated">Locating cited passage…</small></div><span>${I('close')}</span></header><div class="source-paper"><div class="source-brand">WEEKEND READING CLUB</div><h1>Sign-up notes</h1><p class="source-intro">Everything you need to join our next reading afternoon.</p><h2>Registration</h2><p>There are 20 free places for the online event. Sign up with your name and the book you plan to share.</p><p id="sourcePassage"><mark>Registration closes on November 5 at 18:00.</mark><br><mark>Each participant should bring a book and prepare</mark><br><mark>a two-minute recommendation.</mark></p><h2>Before the session</h2><p>Check your meeting link and bring a passage you would like to discuss. We will collect recommendations in a shared reading list.</p><div class="source-page-number">1</div></div><footer>‹　1 / 1　›<span>−　100%　+</span></footer></div>${mouse('citation')}</div>`;
 for(const scene of extraScenes){
  const root=document.createElement('section');root.id='s-'+scene.key;root.className='chapter wide-chapter';
  root.innerHTML=`<div class="wide-heading"><div class="eyebrow">${scene.label}</div><h1>${scene.title}</h1><p>${scene.sub}</p></div><div class="wide-brand"><img src="assets/logo-light.png"><span>v0.9.0</span></div>${html[scene.key]}`;
  $('chapters').appendChild(root);
 }
}

function type(id,input,s,placeholder=''){
 const sent=s>=input.send,count=sent&&!input.retain?0:input.chars.filter(c=>s+1e-7>=c.at).length;
 const el=$(id);el.dataset.chars=count;
 const caret=count>0&&s<input.send?'<span class="caret"></span>':'';
 setHTML(el,count?esc(input.prompt.slice(0,count))+caret:placeholder);el.classList.toggle('typing',count>0);
}
function reveal(id,s,a,d=.35){const el=$(id),p=P(s,a,d);el.style.display=p>0?'':'none';show(el,p,10);}
function point(key,id,s,at,root){
 const cur=$(key+'Cursor'),rip=$(key+'Click'),visible=s>=at-.6&&s<at+.4;
 cur.style.opacity=visible?1:0;rip.style.opacity=0;if(!visible)return;
 const box=$(id).getBoundingClientRect(),rr=root.getBoundingClientRect(),scale=rr.width/root.offsetWidth,p=PI(s,at-.6,.6),x=(box.x-rr.x+box.width*.55)/scale,y=(box.y-rr.y+box.height*.55)/scale;
 cur.style.left=x+100*(1-p)+'px';cur.style.top=y+55*(1-p)+'px';cur.style.opacity=1-P(s,at+.2,.2);
 rip.style.left=x+'px';rip.style.top=y+'px';const q=lin(s,at,at+.4);rip.style.opacity=q>0&&q<1?.65*(1-q):0;rip.style.transform=`scale(${.4+q})`;
}

export function renderExtraScene(key,s,{interactions}){
 const root=$('s-'+key);show(root.querySelector('.wide-heading'),P(s,.12,.6),12);
 const inputs=interactions.filter(i=>i.scene===key);
 if(key==='browser'){
  type('browserIn',inputs[0],s,'Describe a browser task…');type('browser-titleIn',inputs[1],s,'Name your event');type('browser-notesIn',inputs[2],s,'Add preparation notes');
  reveal('browserUser',s,3);[3.25,4,4.65].forEach((a,n)=>{reveal('browserStep'+n,s,a);$('browserStep'+n).classList.toggle('run',s>=a&&s<a+.65)});
  $('eventDate').textContent=s>=6.1?'Nov 7 · 14:00–16:00':'Choose a date';$('eventDate').classList.toggle('filled',s>=6.1);
  $('eventCapacity').textContent=s>=6.35?'20 people':'Add capacity';$('eventCapacity').classList.toggle('filled',s>=6.35);
  reveal('browserLive',s,4.5,.55);stream($('browserAnswer'),'<p>Draft filled. The browser is open for your review.<br>Nothing has been published.</p>',s,9.4,85,false);
  const paused=s>=11.55;$('browserState').textContent=paused?'Paused':'Running';
  setHTML($('browserPause'),I(paused?'play':'pause')+(paused?'Resume':'Pause'));
  $('eventDraftState').textContent=s>=9.2?'Ready for your review · Not published':'Your changes are a draft.';
  $('browser-titleIn').classList.toggle('field-active',s>=5&&s<6.1);$('browser-notesIn').classList.toggle('field-active',s>=6.55&&s<8.9);
  const msg=$('browserUser'),compact=PI(s,4.05,.45);msg.style.maxHeight=(1-compact)*150+'px';msg.style.opacity=1-compact;msg.style.overflow='hidden';msg.style.marginBottom=(1-compact)*20+'px';
  const complete=PI(s,9.05,.4);
  for(let n=0;n<3;n++){
   const row=$('browserStep'+n),detail=row.querySelector('small');
   detail.style.maxHeight=24*(1-complete)+'px';detail.style.opacity=1-complete;detail.style.overflow='hidden';
   row.style.paddingBottom=10-3*complete+'px';
  }
  // The floating preview mirrors the exact authored browser, including focus,
  // typing and pointer state. It is outside the conversation's document flow.
  const phases=[[4.55,'browser-titleIn'],[6.1,'eventDate'],[6.35,'eventCapacity'],[6.55,'browser-notesIn']];
  const active=phases.findLastIndex(([at])=>s>=at),page=$('browserWindow'),pc=$('browserPageCursor'),click=$('browserPageClick');
  pc.style.opacity=s>=4.3&&s<9.15?1:0;click.style.opacity=0;
  if(active>=0){
   const [at,id]=phases[active],before=phases[Math.max(0,active-1)][1];
   const base=page.getBoundingClientRect(),scale=base.width/page.offsetWidth;
   const location=id=>{const r=$(id).getBoundingClientRect();return [(r.left-base.left)/scale+56,(r.top-base.top)/scale+23]};
   const [x,y]=location(id),[px,py]=location(before),p=PI(s,at,.22);
   pc.style.left=px+(x-px)*p+'px';pc.style.top=py+(y-py)*p+'px';
   const q=lin(s,at+.22,at+.62);click.style.left=x+'px';click.style.top=y+'px';click.style.opacity=q>0&&q<1?.4*(1-q):0;click.style.transform=`scale(${.4+q})`;
  }
  setHTML($('browserMirror'),page.outerHTML.replace(/ id="[^"]*"/g,'').replace(/ data-typewriter="[^"]*"/g,'').replace('class="browser-window"','class="browser-window browser-mirror"'));
  $('browserLive').classList.toggle('is-paused',paused);
  point('browser',s<4?'browserSend':'browserPause',s,s<4?3:11.5,root);
 }
 if(key==='gallery'){
  type('galleryIn',inputs[0],s,'Search descriptions and text…');
  const filtered=PI(s,2.7,.65),open=PI(s,4.5,.65);
  $('galleryCount').textContent=filtered>.5?'4 images found':'12 images';
  const chosen=[0,2,3,4],heights={poster:560,cover:520,chart:320,diagram:360,note:360,agenda:360};
  galleryItems.forEach(([kind],n)=>{
   const el=$('galleryTile'+n),c=n%6,row=Math.floor(n/6),index=chosen.indexOf(n),baseWidth=244.6,targetWidth=374.5;
   const baseTop=row?(heights[galleryItems[c][0]]/480*baseWidth+86):0;
   const x=c*(baseWidth+18),y=baseTop,p=index<0?0:filtered;
   el.style.left=(x+(index*(targetWidth+20)-x)*p)+'px';el.style.top=(y*(1-p))+'px';
   el.style.width=(baseWidth+(targetWidth-baseWidth)*p)+'px';el.style.opacity=index<0?1-filtered:1;
   el.style.transform=`scale(${index<0?1-.05*filtered:1})`;
  });
  $('galleryViewer').style.opacity=open;$('galleryViewer').style.visibility=open>0?'visible':'hidden';$('galleryViewer').style.transform=`translateY(${24*(1-open)}px)`;
  $('galleryLargeArt').style.transform=`scale(${.96+.04*open+.012*lin(s,5.3,9.5)})`;
  point('gallery','galleryTile0',s,4.45,$('galleryApp'));
 }
 if(key==='wiki'){
  const focus=PI(s,4.35,.95),read=PI(s,8.45,.65);
  graphGroups.forEach((g,k)=>{
   const el=$('graphGroup'+k);$('wikiGroupLabel'+k).style.opacity=1-focus;
   el.style.opacity=k===0?P(s,.4,.85):P(s,.6+k*.15,.85)*(1-focus);
   if(k===0){const scale=1+.4*focus-.12*read,x=230*focus-135*read,y=5*focus;el.style.transform=`translate(${x}px,${y}px) scale(${scale})`;}
  });
  $('wikiFocus').style.opacity=focus;$('wikiFocus').style.transform=`translateY(${10*(1-focus)}px)`;
  $('wikiNodeCount').textContent=focus>.5?'6 / 16 nodes':'16 / 16 nodes';
  $('wikiReader').style.opacity=read;$('wikiReader').style.transform=`translateX(${580*(1-read)}px)`;
  $('wikiReader').style.visibility=read>0?'visible':'hidden';$('wikiNode0-2').classList.toggle('selected',read>0);
  point('wiki',s<6?'wikiGroupLabel0':'wikiNode0-2',s,s<6?4.3:8.4,$('wikiApp'));
 }
 if(key==='citation'){
  type('citationIn',inputs[0],s,'Ask about your knowledge…');reveal('citationUser',s,2.55);reveal('citationSearch',s,2.9);
  stream($('citationAnswer'),`<p>Registration closes <b>November 5 at 18:00</b>. Bring a book and prepare a <b>two-minute recommendation</b>. <span class="citation-number" id="citationRef">1</span></p><div class="citation-source-label">${I('file')}Sign-up notes.pdf · Page 1</div>`,s,3.55,92,false);
  const pop=P(s,5.2,.25)*(1-P(s,6.4,.2)),open=PI(s,6.55,.65);
  $('citationPop').style.opacity=pop;$('citationPop').style.visibility=pop>0?'visible':'hidden';
  $('citationConversation').style.left=440-340*open+'px';
  $('sourceViewer').style.opacity=open;$('sourceViewer').style.transform=`translateX(${800*(1-open)}px)`;$('sourceViewer').style.visibility=open>0?'visible':'hidden';
  $('sourceLocated').textContent=s>=7.35?'Located cited passage · Page 1':'Locating cited passage…';
  $('sourcePassage').style.setProperty('--highlight',P(s,7.35,.5));
  if(s<4)point('citation','citationSend',s,2.55,$('citationApp'));
  else if($('citationRef'))point('citation','citationRef',s,s<5.65?5.2:6.5,$('citationApp'));
 }
}
