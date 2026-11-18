// Original WeKnora intro-film HTML utilities and icon paths.

const $=id=>document.getElementById(id);
const cl=(x,a=0,b=1)=>Math.min(b,Math.max(a,x));
const lin=(t,a,b)=>cl((t-a)/(b-a));
const eo=x=>1-Math.pow(1-x,3);
const eio=x=>x<.5?4*x*x*x:1-Math.pow(-2*x+2,3)/2;
const P=(t,a,d=.6)=>eo(lin(t,a,a+d));
const PI=(t,a,d=.6)=>eio(lin(t,a,a+d));
const mix=(a,b,p)=>a+(b-a)*p;
function show(el,p,dy=18,dx=0){if(!el)return;el.style.opacity=p;el.style.transform=`translate(${(1-p)*dx}px,${(1-p)*dy}px)`;el.style.visibility=p<=0.001?'hidden':'visible'}
function typed(str,t,a,cps=32){return str.slice(0,Math.floor(cl((t-a)*cps,0,str.length)))}
const tplCache=new Map();
function tpl(html){let r=tplCache.get(html);if(!r){r=document.createElement('div');r.innerHTML=html;tplCache.set(html,r)}return r}
function hlen(html){return tpl(html).textContent.length}
function trunc(html,n){const c=tpl(html).cloneNode(true);let left=n;(function w(node){for(const ch of [...node.childNodes]){if(left<=0){node.removeChild(ch);continue}if(ch.nodeType===3){if(ch.data.length>left){ch.data=ch.data.slice(0,left);left=0}else left-=ch.data.length}else w(ch)}})(c);return c.innerHTML}
function stream(el,html,t,a,cps=90,caret=true){const L=hlen(html),n=Math.floor(cl((t-a)*cps,0,L));const key=n+'|'+html.length;if(el._k===key)return;el._k=key;el.innerHTML=trunc(html,n)+(caret&&n>0&&n<L?'<span class="caret"></span>':'');}
function setHTML(el,h){if(el._h!==h){el._h=h;el.innerHTML=h}}


const IC={
 bulb:'<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-4 10.5c.6.6 1 1.4 1 2.5h6c0-1.1.4-1.9 1-2.5A6 6 0 0 0 12 3z"/>',
 search:'<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>',
 kb:'<path d="M12 3l8 4.5v9L12 21l-8-4.5v-9z"/><path d="M4 7.5l8 4.5 8-4.5M12 12v9"/>',
 file:'<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/>',
 fileq:'<path d="M13 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h4"/><path d="M13 3v5h5v3"/><circle cx="16" cy="16" r="3"/><path d="M20.5 20.5l-2.3-2.3"/>',
 check:'<circle cx="12" cy="12" r="9"/><path d="M8 12.5l2.5 2.5L16 9.5"/>',
 tool:'<path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L4 17l3 3 5.3-5.3a4 4 0 0 0 5.4-5.4l-2.5 2.5-2.5-.5-.5-2.5z"/>',
 mem:'<path d="M12 5a3 3 0 0 0-5.8 1A3 3 0 0 0 4 11a3 3 0 0 0 2 4.5A3 3 0 0 0 12 19zM12 5a3 3 0 0 1 5.8 1A3 3 0 0 1 20 11a3 3 0 0 1-2 4.5A3 3 0 0 1 12 19"/>',
 term:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/>',
 web:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M6.5 6.5h.01M9 6.5h.01"/>',
 skill:'<rect x="4" y="4" width="16" height="16" rx="3"/><path d="M9 10l-2 2 2 2M15 10l2 2-2 2M13 9l-2 6"/>',
 chat:'<path d="M4 5h16v11H9l-5 4z"/><path d="M12 8v5M9.5 10.5h5"/>',
 agent:'<path d="M12 3l2 5 5 2-5 2-2 5-2-5-5-2 5-2z"/>',
 box:'<rect x="3" y="7" width="18" height="13" rx="2"/><path d="M9 7V5h6v2M3 12h18M10 12v2h4v-2"/>',
 share:'<path d="M7 9a3 3 0 1 0 0 6c2 0 3-1.5 5-3s3-3 5-3a3 3 0 1 1 0 6c-2 0-3-1.5-5-3S9 9 7 9z"/>',
 art:'<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 14h6M12 11v6"/>',
 clip:'<path d="M20 11l-8 8a5 5 0 0 1-7-7l8-8a3.5 3.5 0 0 1 5 5l-8 8a2 2 0 0 1-3-3l7-7"/>',
 at:'<circle cx="12" cy="12" r="4"/><path d="M16 12v1.5a2.5 2.5 0 0 0 5 0V12a9 9 0 1 0-3.5 7.1"/>',
 send:'<path d="M5 12l14-7-5 14-2.5-5.5z"/>',
 book:'<path d="M5 4.5A1.5 1.5 0 0 1 6.5 3H19v15H6.5A1.5 1.5 0 0 0 5 19.5zM5 19.5A1.5 1.5 0 0 0 6.5 21H19"/>',
 panel:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>',
 folder:'<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
 desk:'<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
 lock:'<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>',
 concept:'<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-4 10.5c.6.6 1 1.4 1 2.5h6c0-1.1.4-1.9 1-2.5A6 6 0 0 0 12 3z"/>',
 tag:'<path d="M3 12V4h8l10 10-8 8z"/><circle cx="7.5" cy="8.5" r="1.3"/>',
 chev:'<path d="M9 6l6 6-6 6"/>', chevd:'<path d="M6 9l6 6 6-6"/>',
 edit:'<path d="M4 20h4L19 9l-4-4L4 16z"/>', hist:'<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5M12 7v5l3 2"/>',
 graph:'<circle cx="6" cy="6" r="2.5"/><circle cx="18" cy="8" r="2.5"/><circle cx="10" cy="18" r="2.5"/><path d="M8.3 7l7.3.6M7 8.2l2.2 7.4M16.3 10l-4.6 6.2"/>',
 trash:'<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
 api:'<path d="M8 6l-5 6 5 6M16 6l5 6-5 6"/>', widget:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M14 16h4v-3"/><circle cx="16" cy="14" r="4"/>',
 pause:'<path d="M9 6v12M15 6v12"/>', stop:'<rect x="6" y="6" width="12" height="12" rx="2"/>',
 sync:'<path d="M4 12a8 8 0 0 1 14-5.3L20 9M20 4v5h-5M20 12a8 8 0 0 1-14 5.3L4 15M4 20v-5h5"/>',
 rank:'<path d="M4 6h10M4 12h7M4 18h4M17 5v14M14 16l3 3 3-3"/>',
};
const I=(k,cls='i')=>`<svg class="${cls}" viewBox="0 0 24 24">${IC[k]}</svg>`;


export {$,cl,lin,eo,eio,P,PI,mix,show,typed,trunc,stream,setHTML,IC,I};
