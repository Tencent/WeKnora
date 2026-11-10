// Offline media renderer. Uses an empty context and authored SVG only.
import {chromium} from 'playwright-core';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {mkdir,writeFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {dirname,resolve} from 'node:path';
const root=dirname(fileURLToPath(import.meta.url)),out=resolve(root,'../out/release-film');
await mkdir(out,{recursive:true});const mode=process.argv[2]||'stills';
const browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});
const errors=[];const checks=[];
async function page(){const p=await browser.newPage({viewport:{width:1920,height:1080},deviceScaleFactor:1});p.on('pageerror',e=>errors.push(e.message));p.on('console',m=>{if(m.type()==='error')errors.push(m.text())});await p.route('**/*',r=>new URL(r.request().url()).origin==='http://127.0.0.1:3110'?r.continue():r.abort());await p.goto('http://127.0.0.1:3110/?render');await p.waitForFunction(()=>window.film?.ready);return p;}
if(mode==='stills'){
 const p=await page();
 for(const t of (process.argv.slice(3).length?process.argv.slice(3).map(Number):[0,3.3,6.9,10.7,17,21.3,22.7,23.5,25,26.65,27.1,27.7,29,32,35,38.7,41.8,46,50,53,56,59,64,65.7,66.5,68,69.65,70.1,70.7,74,77,80,84,86,87.8,92.5,94.5,97.7,100,103,107,110.8,112,113.8,115.4,118.5,120.5,122.5,124.8,126,127.7,129,130.4,131.4,132.3,135.5,136.4,137.8,140,142.3,145,146.1,147.8,149.2,150.7,152,154.4,155.5,156.3,157.3,159.3,160.4,162,164])){
  await p.evaluate(t=>window.film.seek(t),t);await p.screenshot({path:resolve(out,`frame-${t.toFixed(2)}.png`)});
  checks.push(await p.evaluate(t=>({t,images:[...document.querySelectorAll('img')].map(e=>e.getAttribute('src'))}),t));
 }
 const interactionChecks=[];
 for(const item of await p.evaluate(()=>window.film.interactions)){
  const scene=await p.evaluate(key=>window.film.scenes.find(s=>s.key===key),item.scene);
  for(const [phase,local] of [['typing',(item.start+item.end)/2],['ready',item.send-.08],['sent',item.send+.45]]){
   await p.evaluate(t=>window.film.seek(t),scene.a+local);
   const count=await p.locator(`[data-typewriter="${item.id||item.scene}"]`).getAttribute('data-chars');
   const ok=phase==='typing'?Number(count)>0&&Number(count)<item.prompt.length:phase==='ready'?Number(count)===item.prompt.length:Number(count)===(item.retain?item.prompt.length:0);
   interactionChecks.push({scene:item.scene,phase,characters:Number(count),ok});
  }
 }
 await writeFile(resolve(out,'interaction-checks.json'),JSON.stringify(interactionChecks,null,2));
 if(interactionChecks.some(x=>!x.ok))errors.push('Typewriter, send or clearing check failed');
 await writeFile(resolve(out,'visual-checks.json'),JSON.stringify(checks,null,2));
 await writeFile(resolve(out,'timing.json'),JSON.stringify(await p.evaluate(()=>({duration:window.film.duration,scenes:window.film.scenes,captions:window.film.captions,interactions:window.film.interactions,keyEvents:window.film.keyEvents})),null,2));
 await p.close();
}else{
 const fps=mode==='draft'?24:60;
 const probe=await page(),duration=await probe.evaluate(()=>window.film.duration);await probe.close();
 const starts=[0,55,110],lengths=[55,55,duration-110];
 await Promise.all(starts.map(async(start,part)=>{
  const p=await page();
  const encoder=spawn('/opt/homebrew/bin/ffmpeg',['-v','error','-y','-f','image2pipe','-framerate',String(fps),'-i','-','-c:v','libx264','-preset','fast','-crf',mode==='draft'?'21':'18','-pix_fmt','yuv420p','-color_primaries','bt709','-color_trc','bt709','-colorspace','bt709','-movflags','+faststart','-an',resolve(out,`${mode}-${part}.mp4`)],{stdio:['pipe','ignore','inherit']});
  const done=once(encoder,'close');
  for(let i=0;i<lengths[part]*fps;i++){await p.evaluate(t=>window.film.seek(t),start+i/fps);if(!encoder.stdin.write(await p.screenshot({type:'png'})))await once(encoder.stdin,'drain');if(i&&i%(fps*10)===0)console.log(`Part ${part+1}: ${i/fps}s`);}
  encoder.stdin.end();const [code]=await done;if(code!==0)throw Error('Video encoding failed');await p.close();console.log(`Part ${part+1} complete`);
 }));
 await writeFile(resolve(out,`${mode}-concat.txt`),starts.map((_,i)=>`file '${mode}-${i}.mp4'`).join('\n'));
 const concat=spawn('/opt/homebrew/bin/ffmpeg',['-v','error','-y','-f','concat','-safe','0','-i',resolve(out,`${mode}-concat.txt`),'-c','copy','-movflags','+faststart',resolve(out,`${mode}-silent.mp4`)],{stdio:'inherit'});const [code]=await once(concat,'close');if(code!==0)throw Error('Concatenation failed');
}
await browser.close();await writeFile(resolve(out,`${mode}-errors.json`),JSON.stringify([...new Set(errors)],null,2));if(errors.length)throw Error([...new Set(errors)].join('\n'));console.log(mode+' complete');
