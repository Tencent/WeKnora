// Focused media regression checks: clipped controls, input/caret collisions,
// approval field alignment and exactly synchronized character events.
import {chromium} from 'playwright-core';
import {writeFile,mkdir} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const out=fileURLToPath(new URL('../out/release-film/',import.meta.url));
await mkdir(out,{recursive:true});
const browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
const page=await browser.newPage({viewport:{width:1920,height:1080}});
await page.goto('http://127.0.0.1:3110/?render');await page.waitForFunction(()=>window.film?.ready);
const pageErrors=[];page.on('pageerror',e=>pageErrors.push(e.message));
const report=await page.evaluate(()=>{
 const errors=[],events=window.film.keyEvents;
 function visible(el){for(let n=el;n&&n!==document.body;n=n.parentElement){const s=getComputedStyle(n);if(s.display==='none'||s.visibility==='hidden'||+s.opacity<.15)return false}return true}
 const keyChecks=[];
 for(const i of window.film.interactions){
  const a=window.film.scenes.find(s=>s.key===i.scene).a;
  for(let n=0;n<i.chars.length;n++){
   const t=Math.round((a+i.chars[n].at)*60)/60;
   window.film.seek(t);const count=Number(document.querySelector(`[data-typewriter="${i.id||i.scene}"]`).dataset.chars);
   if(count!==n+1)keyChecks.push({scene:i.scene,t,expected:n+1,actual:count});
  }
 }
 for(let frame=0;frame<window.film.duration*4;frame++){
  const t=frame/4;window.film.seek(t);
  for(const panel of document.querySelectorAll('.assistant')){
   if(!visible(panel)||panel.dataset.entering==='true')continue;const pr=panel.getBoundingClientRect();
   if(pr.x<30||pr.right>1890||pr.y<30||pr.bottom>1050)errors.push({t,type:'panelBounds',id:panel.id});
   const ph=panel.querySelector('.ph'),bar=panel.querySelector('.bar');
   if(ph.getBoundingClientRect().bottom>bar.getBoundingClientRect().top+1)errors.push({t,type:'inputFooterCollision',id:panel.id});
   for(const el of panel.querySelectorAll('.approval,.inbox,.assistant-header,.source-popover')){
    if(!visible(el))continue;const r=el.getBoundingClientRect();
    if(el.scrollWidth>el.clientWidth+1)errors.push({t,type:'horizontalOverflow',class:el.className});
    if(r.left<pr.left-1||r.right>pr.right+1)errors.push({t,type:'controlOutsidePanel',class:el.className});
   }
   for(const dt of panel.querySelectorAll('.approval dt')){
    if(!visible(dt))continue;const dd=dt.nextElementSibling;const range=document.createRange();range.selectNodeContents(dt);
    if(range.getBoundingClientRect().right>dd.getBoundingClientRect().left-7)errors.push({t,type:'approvalFieldCollision',label:dt.textContent});
   }
  }
  for(const el of document.querySelectorAll('.create-dialog,.wide-app,.browser-task,#browserWindow,.browser-live,.rename-list')){
   if(!visible(el))continue;const r=el.getBoundingClientRect();
   if(r.left<30||r.right>1890||r.top<30||r.bottom>1050)errors.push({t,type:'windowBounds',id:el.id});
  }
  for(const el of document.querySelectorAll('.dialog-textarea,.image-info,.wiki-markdown,.source-paper,.browser-chat')){
   if(!visible(el))continue;
   if(el.scrollHeight>el.clientHeight+1)errors.push({t,type:'verticalOverflow',class:el.className,id:el.id,overflow:el.scrollHeight-el.clientHeight});
   if(el.scrollWidth>el.clientWidth+1)errors.push({t,type:'horizontalOverflow',class:el.className,id:el.id});
  }
  for(const el of document.querySelectorAll('.editorial')){
   if(visible(el)&&el.getBoundingClientRect().bottom>925)errors.push({t,type:'editorialOverflow',id:el.id});
  }
  const subtitle=document.getElementById('subtitle');
  if(visible(subtitle)){
   const r=subtitle.getBoundingClientRect();
   if(r.top<1025||r.bottom>1070||r.left<75||r.right>1845||subtitle.scrollWidth>subtitle.clientWidth+1)errors.push({t,type:'subtitleBounds',text:subtitle.textContent});
   if(/^[.,!?]/.test(subtitle.textContent))errors.push({t,type:'subtitlePunctuation',text:subtitle.textContent});
  }
  if(t>=117.5&&t<127){
   const original=document.getElementById('browserWindow'),mirror=document.querySelector('#browserMirror .browser-mirror');
   if(!mirror||original.textContent!==mirror.textContent)errors.push({t,type:'browserPreviewMismatch'});
   for(const kind of ['.form-field','.pointer','.ripple']){
    const a=[...original.querySelectorAll(kind)],b=[...mirror.querySelectorAll(kind)];
    if(a.some((el,n)=>el.getAttribute('class')!==b[n]?.getAttribute('class')||el.getAttribute('style')!==b[n]?.getAttribute('style')))errors.push({t,type:'browserPreviewStateMismatch',kind});
   }
   const floating=document.getElementById('browserLive');
   if(floating.closest('.browser-chat')||floating.getBoundingClientRect().width>400)errors.push({t,type:'browserPreviewNotFloating'});
  }
  if(t>=98&&t<101&&getComputedStyle(document.getElementById('kbNewName')).opacity<.99)errors.push({t,type:'renameNotVisible'});
 }
 for(const at of [94,98.2]){
  window.film.seek(at);const old=document.getElementById('kbOldName'),updated=document.getElementById('kbNewName');
  if((at<98?old:updated).textContent!==(at<98?'Weekend reading club':'Reading club workspace'))errors.push({t:at,type:'renameText'});
 }
 return {sampledFrames:window.film.duration*4,characterEvents:events.length,characterAlignmentErrors:keyChecks,layoutErrors:errors,images:[...new Set([...document.images].map(i=>i.getAttribute('src')))]};
});
report.pageErrors=pageErrors;
await writeFile(out+'layout-checks.json',JSON.stringify(report,null,2));
await browser.close();console.log(JSON.stringify(report,null,2));
if(report.layoutErrors.length||report.characterAlignmentErrors.length||pageErrors.length)process.exitCode=1;
