package main

const cakeScript = `
(function(){
'use strict';
if(window.__cakeClean){try{window.__cakeClean();}catch(e){}}
var S=window.__CAKE||{};S.t0=Date.now();S.wait=S.wait_sec||0;
function $(i){return document.getElementById(i);}
var stage=$('cake-stage'),fx=$('cake-fx'),cta=$('cake-cta'),dimEl=$('cake-dim');
if(!stage||!fx||!cta)return;

var SECT=12,ANG=Math.PI*2/SECT,PI2=Math.PI*2;
var LITE=(navigator.hardwareConcurrency||4)<=4||(navigator.deviceMemory||4)<=2;
var dead=false,raf=0,timers=[],micStop=null,ready=false,busy=false,pending=false;
var R=null,scene,cam,cakeG,banner,warm,amb,sun,sparkles,confGeo,confPts,tgt,tgtG;
var wedges=[],hits=[],lit=[],balloons=[],puffs=[],GEO=[],M={},CAPS=[];
var missing=(((S.total||0)%SECT)+SECT)%SECT;
var palKey='strawberry',autoOn=true,dark=false,tuneOn=false,wishShown=false;
var pitch=.42,pitchG=.42,zoom=1,zoomG=1,vel=0,drag=null,last=performance.now(),actx=null;
var popped=parseInt(S.popped,10)||0;
var TIERS=[{r:2.2,h:.9,y:.1},{r:1.6,h:.8,y:1.0},{r:1.05,h:.7,y:1.8}];
var PAL={
 strawberry:{sponge:0xf7d29c,frost:0xfff1f6,frost2:0xf9a8d4,jam:0xe11d48,pearl:0xfde68a,fruit:0xe11d48},
 choco:{sponge:0x5a3520,frost:0x8b5a3c,frost2:0x4a2a17,jam:0x9a3412,pearl:0xfde68a,fruit:0xb91c1c},
 vanilla:{sponge:0xfcd9a0,frost:0xfffbeb,frost2:0xfde68a,jam:0xfb923c,pearl:0xfbcfe8,fruit:0xf43f5e},
 blueberry:{sponge:0xe9d5b0,frost:0xeef2ff,frost2:0xa5b4fc,jam:0x4f46e5,pearl:0xfde047,fruit:0x4338ca}
};
var CONF=[0xf43f5e,0xf59e0b,0x8b5cf6,0x06b6d4,0x22c55e,0xec4899,0xfacc15];
var BCOL=[0xf43f5e,0xf59e0b,0x8b5cf6,0x06b6d4,0x22c55e,0xec4899,0x3b82f6];

window.__cakeClean=function(){
 dead=true;cancelAnimationFrame(raf);timers.forEach(clearInterval);
 document.body.removeEventListener('htmx:beforeRequest',onBefore);
 document.body.removeEventListener('htmx:afterRequest',onAfter);
 window.removeEventListener('resize',onResize);
 if(micStop)micStop();
 try{if(R){R.dispose();}}catch(e){}
 var bl=document.getElementById('bdy-layer');if(bl)bl.style.display='';
};
(function(){var bl=document.getElementById('bdy-layer');if(bl)bl.style.display='none';})();

/* ───────── helpers ───────── */
function sleep(ms){return new Promise(function(r){setTimeout(r,ms);});}
function tween(a,b,ms,cb){return new Promise(function(res){var t0=performance.now();function f(n){var k=Math.min(1,(n-t0)/ms),e=k<.5?2*k*k:1-Math.pow(-2*k+2,2)/2;cb(a+(b-a)*e);if(k<1&&!dead)requestAnimationFrame(f);else res();}requestAnimationFrame(f);});}
function ind(n){n=Math.round(n);var s=String(n);if(s.length<=3)return s;var l=s.slice(-3),r=s.slice(0,-3);return r.replace(/\B(?=(\d{2})+(?!\d))/g,',')+','+l;}
function hexs(n){return '#'+('000000'+n.toString(16)).slice(-6);}
function remaining(){return Math.max(0,Math.ceil((S.wait||0)-(Date.now()-S.t0)/1000));}
function hms(s){var h=Math.floor(s/3600),m=Math.floor(s%3600/60),x=s%60;return (h<10?'0':'')+h+':'+(m<10?'0':'')+m+':'+(x<10?'0':'')+x;}
function mat(c,r){return LITE?new THREE.MeshLambertMaterial({color:c}):new THREE.MeshStandardMaterial({color:c,roughness:r==null?.7:r});}
function matT(t,r){return LITE?new THREE.MeshLambertMaterial({map:t}):new THREE.MeshStandardMaterial({map:t,roughness:r||.6});}
function ctex(w,h,draw){var c=document.createElement('canvas');c.width=w;c.height=h;draw(c.getContext('2d'),w,h);return new THREE.CanvasTexture(c);}

/* ───────── audio ───────── */
function ensureAudio(){try{if(!actx){var AC=window.AudioContext||window.webkitAudioContext;if(AC)actx=new AC();}if(actx&&actx.state==='suspended')actx.resume();}catch(e){}return actx;}
function tone(f,d,type,v,when,f2){if(!actx)return;try{var t=actx.currentTime+(when||0),o=actx.createOscillator(),g=actx.createGain();o.type=type||'sine';o.frequency.setValueAtTime(f,t);if(f2)o.frequency.exponentialRampToValueAtTime(f2,t+d);g.gain.setValueAtTime(.0001,t);g.gain.exponentialRampToValueAtTime(v||.2,t+.02);g.gain.exponentialRampToValueAtTime(.0001,t+d);o.connect(g);g.connect(actx.destination);o.start(t);o.stop(t+d+.05);}catch(e){}}
function noise(d,v,lp){if(!actx)return;try{var n=Math.floor(actx.sampleRate*d),b=actx.createBuffer(1,n,actx.sampleRate),c=b.getChannelData(0);for(var i=0;i<n;i++)c[i]=(Math.random()*2-1)*Math.pow(1-i/n,2);var s=actx.createBufferSource();s.buffer=b;var f=actx.createBiquadFilter();f.type='lowpass';f.frequency.value=lp||1500;var g=actx.createGain();g.gain.value=v||.2;s.connect(f);f.connect(g);g.connect(actx.destination);s.start();}catch(e){}}
function sNom(){tone(210,.14,'sine',.28,0,110);}
function sWhoosh(){noise(.35,.28,2200);}
function sPuff(){noise(.22,.2,900);}
function sPop(){noise(.09,.5,4000);}
function sDing(){tone(1320,.25,'sine',.16);tone(1760,.4,'sine',.13,.09);tone(2093,.5,'sine',.1,.18);}
function sCheer(){[523,659,784,1047].forEach(function(f,i){tone(f,.3,'triangle',.16,i*.09);});}
var TUNE=[[392,.75],[392,.25],[440,1],[392,1],[523,1],[494,2],[392,.75],[392,.25],[440,1],[392,1],[587,1],[523,2],[392,.75],[392,.25],[784,1],[659,1],[523,1],[494,1],[440,2],[698,.75],[698,.25],[659,1],[523,1],[587,1],[523,2]];

/* ───────── 2D overlay effects (inline styles + Web Animations) ───────── */
function fxEl(txt,css){var e=document.createElement('div');e.style.cssText='position:absolute;left:0;top:0;pointer-events:none;white-space:nowrap;'+css;if(txt)e.textContent=txt;fx.appendChild(e);return e;}
function floatText(txt,x,y,css,ms){
 var e=fxEl(txt,'font:900 20px system-ui,sans-serif;color:#fff;text-shadow:0 2px 0 rgba(0,0,0,.45),0 0 10px rgba(236,72,153,.85);'+(css||''));
 if(!e.animate){setTimeout(function(){e.remove();},ms||1800);return;}
 var a=e.animate([{transform:'translate('+x+'px,'+y+'px) translateX(-50%) scale(.6)',opacity:0},{transform:'translate('+x+'px,'+(y-24)+'px) translateX(-50%) scale(1.1)',opacity:1,offset:.2},{transform:'translate('+x+'px,'+(y-90)+'px) translateX(-50%) scale(1)',opacity:0}],{duration:ms||1800,easing:'ease-out'});
 a.onfinish=function(){e.remove();};
}
function crumbs(x,y,n){
 var p=PAL[palKey];
 for(var i=0;i<n;i++){(function(){
  var sz=3+Math.random()*4,e=fxEl('','width:'+sz+'px;height:'+sz+'px;border-radius:2px;background:'+hexs(i%3?p.sponge:p.frost2)+';');
  if(!e.animate){e.remove();return;}
  var dx=(Math.random()-.5)*130,dy=-30-Math.random()*70;
  var a=e.animate([{transform:'translate('+x+'px,'+y+'px)',opacity:1},{transform:'translate('+(x+dx)+'px,'+(y+dy)+'px)',opacity:1,offset:.4},{transform:'translate('+(x+dx*1.2)+'px,'+(y+90)+'px)',opacity:0}],{duration:800});
  a.onfinish=function(){e.remove();};
 })();}
}
function coinRain(){
 var w=stage.clientWidth,h=stage.clientHeight;
 for(var i=0;i<28;i++){(function(i){setTimeout(function(){
  if(dead)return;
  var x=Math.random()*w,e=fxEl('🪙','font-size:24px;line-height:1;');
  if(!e.animate){e.remove();return;}
  var a=e.animate([{transform:'translate('+x+'px,-30px) rotate(0deg)',opacity:1},{transform:'translate('+(x+(Math.random()-.5)*60)+'px,'+(h-30)+'px) rotate(540deg)',opacity:1,offset:.85},{transform:'translate('+x+'px,'+(h-20)+'px) rotate(600deg)',opacity:0}],{duration:1500+Math.random()*900,easing:'ease-in'});
  a.onfinish=function(){e.remove();};
 },i*70);})(i);}
}
function scr(v){cam.updateMatrixWorld();var p=v.clone().project(cam);return{x:(p.x+1)/2*stage.clientWidth,y:(1-p.y)/2*stage.clientHeight};}

/* ───────── UI ───────── */
function msg(t,kind){var m=$('cake-msg');if(!m)return;m.textContent=t;var c={ok:['rgba(34,197,94,.14)','rgba(34,197,94,.5)'],err:['rgba(239,68,68,.12)','rgba(239,68,68,.5)'],info:['rgba(139,92,246,.13)','rgba(139,92,246,.5)']}[kind]||['rgba(128,128,128,.08)','rgba(128,128,128,.2)'];m.style.background=c[0];m.style.borderColor=c[1];}
function updateStats(){$('cake-mine').textContent=ind(S.mine||0);$('cake-total').textContent=ind(S.total||0);$('cake-pts').textContent=ind(S.points||0);}
function row(href,l,r){var a=document.createElement(href?'a':'div');if(href)a.href=href;a.style.cssText='display:flex;align-items:center;justify-content:space-between;gap:8px;padding:8px 12px;border-radius:12px;background:rgba(128,128,128,.08);text-decoration:none;color:inherit;font-size:12.5px;font-weight:800;';var x=document.createElement('span');x.style.cssText='min-width:0;overflow:hidden;text-overflow:ellipsis;';x.textContent=l;var y=document.createElement('span');y.style.cssText='font-size:10.5px;opacity:.65;font-weight:700;flex-shrink:0;';y.textContent=r;a.appendChild(x);a.appendChild(y);return a;}
function empty(box,t){var e=document.createElement('div');e.style.cssText='font-size:12px;opacity:.6;text-align:center;padding:8px;';e.textContent=t;box.appendChild(e);}
function renderLists(){
 var b=$('cake-guests'),t=$('cake-top'),w=$('cake-wishes'),i;
 if(b){b.innerHTML='';var g=S.recent||[];if(!g.length)empty(b,'Abhi kisi ne slice nahi khai — pehle aap banein! 🍰');g.forEach(function(x){b.appendChild(row('/profile/'+encodeURIComponent(x.u),'🍰 '+x.u,x.t));});}
 if(t){t.innerHTML='';var tp=S.top||[],med=['🥇','🥈','🥉','4️⃣','5️⃣'];if(!tp.length)empty(t,'Abhi koi champion nahi 🏆');tp.forEach(function(x,i){t.appendChild(row('/profile/'+encodeURIComponent(x.u),med[i]+' '+x.u,x.n+' slices'));});}
 if(w){w.innerHTML='';var ws=S.wishes||[];if(!ws.length)empty(w,'Pehli wish aap likhein! 🏮');ws.forEach(function(x){w.appendChild(row('','🏮 '+x.w,x.u+' · '+x.t));});}
}
function syncCTA(){
 var t=$('cake-cta-txt'),rem=remaining(),ok=!!S.open&&!busy&&rem<=0;
 if(!S.open)t.textContent='🔒 Cake abhi band hai';
 else if(busy)t.textContent='🍴 Nosh farmaiye…';
 else if(rem>0)t.textContent='⏳ Agli slice: '+hms(rem);
 else t.textContent='Slice Khayein · +10,00,000 pts';
 cta.disabled=!ok;
 cta.style.backgroundImage=ok?'linear-gradient(100deg,#ec4899,#f59e0b)':'linear-gradient(100deg,#6b7280,#4b5563)';
 cta.style.opacity=ok?'1':'.85';cta.style.cursor=ok?'pointer':'not-allowed';cta.style.boxShadow=ok?'0 12px 26px -10px rgba(236,72,153,.85)':'none';
}
function showPop(){var e=$('cake-pop');if(!e||!popped)return;e.textContent='🎈 Popped: '+popped;e.style.opacity='1';}

/* ───────── 3D: load + init ───────── */
function loadThree(cb){
 if(window.THREE){cb();return;}
 var urls=['https://cdnjs.cloudflare.com/ajax/libs/three.js/r128/three.min.js','https://cdn.jsdelivr.net/npm/three@0.128.0/build/three.min.js','https://unpkg.com/three@0.128.0/build/three.min.js'],i=0,done=false;
 function next(){
  if(done)return;
  if(i>=urls.length){done=true;cb(true);return;}
  var s=document.createElement('script');s.src=urls[i++];
  var t=setTimeout(function(){s.onload=s.onerror=null;next();},7000);
  s.onload=function(){clearTimeout(t);if(window.THREE&&!done){done=true;cb();}else next();};
  s.onerror=function(){clearTimeout(t);next();};
  document.head.appendChild(s);
 }
 next();
}
var flameTex,smokeTex,dotTex,G_PEARL,G_DRIP,G_FRUIT,G_CAND,G_WICK,G_HIT,G_BAL,G_STR,G_PLATE,HITMAT,M_DARK,M_GREY,M_WHITE,G_KNOT,BTEX={};
var ray,mv,CN=LITE?180:360,cpos,ccol,cvel,cc=0,tcol;

function init3D(){
 if(!window.THREE)return false;
 var w=stage.clientWidth,h=stage.clientHeight;
 try{R=new THREE.WebGLRenderer({antialias:!LITE,alpha:true});}catch(e){R=null;return false;}
 R.setPixelRatio(Math.min(window.devicePixelRatio||1,LITE?1.5:2));R.setSize(w,h,false);
 var cv=R.domElement;cv.style.cssText='position:absolute;left:0;top:0;width:100%;height:100%;display:block;touch-action:pan-y;';
 stage.insertBefore(cv,stage.firstChild);
 scene=new THREE.Scene();cam=new THREE.PerspectiveCamera(42,w/h,.1,100);
 tgt=new THREE.Vector3(0,1.5,0);tgtG=tgt.clone();
 amb=new THREE.AmbientLight(0xffe8f4,.75);scene.add(amb);
 sun=new THREE.DirectionalLight(0xffffff,.85);sun.position.set(4,9,6);scene.add(sun);
 warm=new THREE.PointLight(0xffb45a,.5,10);warm.position.set(0,3.8,0);scene.add(warm);
 ray=new THREE.Raycaster();mv=new THREE.Vector2();tcol=new THREE.Color();

 flameTex=ctex(64,64,function(g,w,h){var gr=g.createRadialGradient(w/2,h*.6,2,w/2,h*.6,w*.48);gr.addColorStop(0,'rgba(255,255,255,1)');gr.addColorStop(.3,'rgba(253,230,138,.95)');gr.addColorStop(.65,'rgba(251,146,60,.55)');gr.addColorStop(1,'rgba(239,68,68,0)');g.fillStyle=gr;g.fillRect(0,0,w,h);});
 smokeTex=ctex(64,64,function(g,w,h){var gr=g.createRadialGradient(w/2,h/2,1,w/2,h/2,w/2);gr.addColorStop(0,'rgba(226,232,240,.9)');gr.addColorStop(1,'rgba(226,232,240,0)');g.fillStyle=gr;g.fillRect(0,0,w,h);});
 dotTex=ctex(32,32,function(g,w,h){var gr=g.createRadialGradient(16,16,0,16,16,16);gr.addColorStop(0,'rgba(255,255,255,1)');gr.addColorStop(1,'rgba(255,255,255,0)');g.fillStyle=gr;g.fillRect(0,0,w,h);});

 G_PEARL=new THREE.SphereGeometry(.07,8,6);G_DRIP=new THREE.SphereGeometry(.07,8,6);G_FRUIT=new THREE.SphereGeometry(.13,10,8);
 G_CAND=new THREE.CylinderGeometry(.05,.05,.5,10);G_WICK=new THREE.CylinderGeometry(.008,.008,.07,4);G_HIT=new THREE.SphereGeometry(.26,8,6);
 G_BAL=new THREE.SphereGeometry(.4,14,10);G_STR=new THREE.CylinderGeometry(.008,.008,1.4,4);G_PLATE=new THREE.CylinderGeometry(1.9,1.9,.06,32);G_KNOT=new THREE.ConeGeometry(.07,.14,8);
 HITMAT=new THREE.MeshBasicMaterial({transparent:true,opacity:0,depthWrite:false});
 M_DARK=mat(0x1f2937,.9);M_GREY=mat(0xcbd5e1,.9);M_WHITE=mat(0xf8fafc,.3);

 var t=new THREE.Mesh(new THREE.CylinderGeometry(6.5,6.5,.2,48),mat(0x3b0764,.8));t.position.y=-.14;scene.add(t);
 var pl=new THREE.Mesh(new THREE.CylinderGeometry(3.1,3.25,.14,48),mat(0xfacc15,.35));pl.position.y=.03;scene.add(pl);
 cakeG=new THREE.Group();scene.add(cakeG);

 banner=new THREE.Sprite(new THREE.SpriteMaterial({map:ctex(1024,220,function(g,w,h){
  g.textAlign='center';g.textBaseline='middle';g.font='900 110px system-ui,Arial,sans-serif';
  var gr=g.createLinearGradient(0,0,w,0);gr.addColorStop(0,'#f472b6');gr.addColorStop(.5,'#fde047');gr.addColorStop(1,'#a78bfa');
  g.lineWidth=16;g.strokeStyle='rgba(60,0,60,.85)';g.strokeText('HAPPY BIRTHDAY '+(S.name||'Denz').toUpperCase(),w/2,h/2);
  g.fillStyle=gr;g.fillText('HAPPY BIRTHDAY '+(S.name||'Denz').toUpperCase(),w/2,h/2);
 }),transparent:true,depthWrite:false}));
 banner.scale.set(5.2,1.12,1);banner.position.set(0,4.3,0);scene.add(banner);

 var sp=new Float32Array(90*3),i;
 for(i=0;i<90;i++){var a=Math.random()*PI2,r=3+Math.random()*5;sp[i*3]=Math.cos(a)*r;sp[i*3+1]=Math.random()*6;sp[i*3+2]=Math.sin(a)*r-1;}
 var sg=new THREE.BufferGeometry();sg.setAttribute('position',new THREE.BufferAttribute(sp,3));
 sparkles=new THREE.Points(sg,new THREE.PointsMaterial({size:.16,map:dotTex,color:0xfff3b0,transparent:true,depthWrite:false,blending:THREE.AdditiveBlending}));
 scene.add(sparkles);

 cpos=new Float32Array(CN*3);ccol=new Float32Array(CN*3);cvel=new Float32Array(CN*3);
 for(i=0;i<CN;i++)cpos[i*3+1]=-99;
 confGeo=new THREE.BufferGeometry();confGeo.setAttribute('position',new THREE.BufferAttribute(cpos,3));confGeo.setAttribute('color',new THREE.BufferAttribute(ccol,3));
 confPts=new THREE.Points(confGeo,new THREE.PointsMaterial({size:.16,vertexColors:true,transparent:true,depthWrite:false}));
 confPts.frustumCulled=false;scene.add(confPts);

 for(i=0;i<(LITE?5:7);i++)addBalloon();
 return true;
}

/* ───────── cake construction ───────── */
function ext(r,a0,a1,h){
 var s=new THREE.Shape();s.moveTo(0,0);s.lineTo(Math.cos(a0)*r,Math.sin(a0)*r);s.absarc(0,0,r,a0,a1,false);s.lineTo(0,0);
 var g=new THREE.ExtrudeGeometry(s,{depth:h,bevelEnabled:false,curveSegments:6});g.rotateX(-Math.PI/2);return g;
}
function cyl(r,h,a0,a1){return new THREE.CylinderGeometry(r,r,h,8,1,true,a0+Math.PI/2,a1-a0);}
function geo(i,k){
 var c=GEO[i]=GEO[i]||[];if(c[k])return c[k];
 var t=TIERS[i],a0=k*ANG,a1=a0+ANG;
 c[k]={body:ext(t.r,a0,a1,t.h),slab:ext(t.r+.03,a0,a1,.12),band:cyl(t.r+.02,t.h*.34,a0,a1),jam:cyl(t.r+.012,.05,a0,a1)};
 return c[k];
}
function capTex(i,p){
 var r=TIERS[i].r+.03,c=document.createElement('canvas');c.width=c.height=512;
 var g=c.getContext('2d'),j,a;
 g.fillStyle=hexs(p.frost);g.fillRect(0,0,512,512);
 g.strokeStyle=hexs(p.frost2);g.lineWidth=5;
 for(j=0;j<3;j++){g.beginPath();g.arc(256,256,246-j*14,0,PI2);g.stroke();}
 for(j=0;j<36;j++){a=j/36*PI2;g.fillStyle=hexs(p.frost2);g.beginPath();g.arc(256+Math.cos(a)*222,256+Math.sin(a)*222,10,0,PI2);g.fill();g.fillStyle='#fff';g.beginPath();g.arc(254+Math.cos(a)*222,254+Math.sin(a)*222,3.5,0,PI2);g.fill();}
 if(i===2){
  g.textAlign='center';g.textBaseline='middle';
  g.font='900 150px "Brush Script MT","Segoe Script","Lucida Handwriting",cursive';
  g.lineWidth=14;g.strokeStyle='#ffffff';g.strokeText(S.name||'Denz',256,240);
  g.fillStyle='#be185d';g.fillText(S.name||'Denz',256,240);
  g.font='48px serif';g.fillStyle='#e11d48';g.fillText('♥  ♥  ♥',256,350);
 }else{
  var cols=['#f43f5e','#facc15','#22c55e','#3b82f6','#a855f7','#f97316'];
  g.lineCap='round';g.lineWidth=5;
  for(j=0;j<170;j++){a=Math.random()*PI2;var d=Math.sqrt(Math.random())*200,x=256+Math.cos(a)*d,y=256+Math.sin(a)*d,b=Math.random()*3.14;g.strokeStyle=cols[j%6];g.beginPath();g.moveTo(x,y);g.lineTo(x+Math.cos(b)*11,y+Math.sin(b)*11);g.stroke();}
 }
 var t=new THREE.CanvasTexture(c);t.repeat.set(1/(2*r),1/(2*r));t.offset.set(.5,.5);return t;
}
function setMats(){
 var p=PAL[palKey];
 M={sponge:mat(p.sponge,.95),frost:mat(p.frost,.5),jam:mat(p.jam,.4),pearl:mat(p.pearl,.3),fruit:mat(p.fruit,.45)};
 CAPS=[0,1,2].map(function(i){return [matT(capTex(i,p),.55),M.frost];});
}
function makeWedge(k){
 var g=new THREE.Group(),a0=k*ANG,ac=a0+ANG/2,dx=Math.cos(ac),dz=-Math.sin(ac);
 g.userData={k:k,dx:dx,dz:dz,a0:a0,a1:a0+ANG,flame:null,candle:null};
 TIERS.forEach(function(t,i){
  var G=geo(i,k),m;
  m=new THREE.Mesh(G.body,[M.sponge,M.sponge]);m.position.y=t.y;g.add(m);
  m=new THREE.Mesh(G.slab,CAPS[i]);m.position.y=t.y+t.h-.02;g.add(m);
  m=new THREE.Mesh(G.band,M.frost);m.position.y=t.y+t.h-t.h*.17;g.add(m);
  m=new THREE.Mesh(G.jam,M.jam);m.position.y=t.y+t.h*.45;g.add(m);
  if(!LITE){
   [.28,.72].forEach(function(f){
    var a=a0+ANG*f,pe=new THREE.Mesh(G_PEARL,M.pearl);
    pe.position.set(Math.cos(a)*(t.r+.05),t.y+.06,-Math.sin(a)*(t.r+.05));g.add(pe);
    var dr=new THREE.Mesh(G_DRIP,M.frost);dr.scale.y=1.6;
    dr.position.set(Math.cos(a+.05)*(t.r+.03),t.y+t.h-t.h*.34,-Math.sin(a+.05)*(t.r+.03));g.add(dr);
   });
  }
  if(i<2){var fr=new THREE.Mesh(G_FRUIT,M.fruit),rr=t.r-.3;fr.position.set(dx*rr,t.y+t.h+.17,dz*rr);g.add(fr);}
 });
 if(k%3!==1){
  var cx=dx*.62,cz=dz*.62,cols=[0xf43f5e,0x3b82f6,0x22c55e,0xf59e0b];
  var cn=new THREE.Mesh(G_CAND,mat(cols[k%4],.6));cn.position.set(cx,2.85,cz);g.add(cn);
  var wk=new THREE.Mesh(G_WICK,M_DARK);wk.position.set(cx,3.13,cz);g.add(wk);
  var fl=new THREE.Sprite(new THREE.SpriteMaterial({map:flameTex,blending:THREE.AdditiveBlending,transparent:true,depthWrite:false}));
  fl.scale.set(.34,.52,1);fl.position.set(cx,3.4,cz);fl.raycast=function(){};
  var ht=new THREE.Mesh(G_HIT,HITMAT);ht.position.set(cx,3.2,cz);ht.userData={hit:true,k:k};
  g.add(fl);g.add(ht);
  if(lit[k]===undefined)lit[k]=true;fl.visible=!!lit[k];
  g.userData.flame=fl;g.userData.candle=[cn,wk,fl,ht];hits.push(ht);
 }
 return g;
}
function buildCake(){
 while(cakeG.children.length)cakeG.remove(cakeG.children[0]);
 wedges=[];hits=[];setMats();
 for(var k=missing;k<SECT;k++){var w=makeWedge(k);wedges[k]=w;cakeG.add(w);}
 updateHud();
}
function candKs(){var a=[];for(var k=0;k<SECT;k++){if(wedges[k]&&wedges[k].userData.flame)a.push(k);}return a;}
function litN(){return candKs().filter(function(k){return lit[k];}).length;}
function updateHud(){var cn=$('cake-no');if(cn)cn.textContent=Math.floor((S.total||0)/SECT)+1;var a=$('cake-left'),b=$('cake-candles');if(a)a.textContent=Math.max(0,SECT-missing);if(b)b.textContent=litN()+'/'+candKs().length;}

/* ───────── confetti / smoke / balloons ───────── */
function burst(x,y,z,n,cols){
 if(!R)return;cols=cols||CONF;
 for(var i=0;i<n;i++){var o=((cc++)%CN)*3;
  cpos[o]=x+(Math.random()-.5)*.4;cpos[o+1]=y;cpos[o+2]=z+(Math.random()-.5)*.4;
  cvel[o]=(Math.random()-.5)*6;cvel[o+1]=3+Math.random()*6;cvel[o+2]=(Math.random()-.5)*6;
  tcol.setHex(cols[i%cols.length]);ccol[o]=tcol.r;ccol[o+1]=tcol.g;ccol[o+2]=tcol.b;}
 confGeo.attributes.position.needsUpdate=true;confGeo.attributes.color.needsUpdate=true;
}
function puff(sp){
 var p=new THREE.Vector3();sp.getWorldPosition(p);
 for(var i=0;i<3;i++){var s=new THREE.Sprite(new THREE.SpriteMaterial({map:smokeTex,transparent:true,opacity:.7,depthWrite:false}));s.position.copy(p);s.scale.set(.15,.15,1);s.visible=false;scene.add(s);puffs.push({s:s,age:-i*.12,vx:(Math.random()-.5)*.4});}
}
function balTex(col){
 if(BTEX[col])return BTEX[col];
 var t=ctex(LITE?512:1024,LITE?256:512,function(g,w,h){
  g.fillStyle=hexs(col);g.fillRect(0,0,w,h);
  var gr=g.createRadialGradient(w*.22,h*.4,4,w*.25,h*.5,h*.65);
  gr.addColorStop(0,'rgba(255,255,255,.4)');gr.addColorStop(1,'rgba(255,255,255,0)');
  g.fillStyle=gr;g.fillRect(0,0,w,h);
  g.textAlign='center';g.textBaseline='middle';g.lineJoin='round';
  var nm=String(S.name||'Denz').toUpperCase();
  [.25,.75].forEach(function(u){
   var cx=w*u;
   function line(txt,y,px){g.font='900 '+px+'px "Trebuchet MS",Arial,sans-serif';g.lineWidth=px*.16;g.strokeStyle='rgba(0,0,0,.4)';g.strokeText(txt,cx,y);g.fillStyle='#fff';g.fillText(txt,cx,y);}
   line('HAPPY',h*.27,h*.085);line('BIRTHDAY',h*.38,h*.075);line(nm,h*.53,h*.165);line('2 OCT',h*.70,h*.08);
  });
 });
 BTEX[col]=t;return t;
}
function addBalloon(){
 if(balloons.length>=12)return;
 var col=BCOL[Math.floor(Math.random()*BCOL.length)],b=new THREE.Mesh(G_BAL,LITE?new THREE.MeshLambertMaterial({map:balTex(col)}):new THREE.MeshPhongMaterial({map:balTex(col),shininess:90,specular:0x999999}));
 b.scale.set(1,1.22,1);var s=new THREE.Mesh(G_STR,M_GREY);s.position.y=-1.25;b.add(s);var kn=new THREE.Mesh(G_KNOT,mat(col,.5));kn.position.y=-.45;b.add(kn);
 var x=(Math.random()<.5?-1:1)*(1.6+Math.random()*2),y=.9+Math.random()*4,z=-1.5-Math.random()*2.5;
 b.position.set(x,y,z);b.userData={balloon:true,bx:x,by:y,bz:z,ph:Math.random()*6.28,col:col};
 scene.add(b);balloons.push(b);
}
function popBalloon(b){
 var i=balloons.indexOf(b);if(i<0)return;balloons.splice(i,1);scene.remove(b);
 burst(b.position.x,b.position.y,b.position.z,26,[b.userData.col,0xfde047,0xffffff]);
 ensureAudio();sPop();try{if(navigator.vibrate)navigator.vibrate(15);}catch(e){}
 popped++;if(window.bdayReportPop)window.bdayReportPop(1);showPop();
 setTimeout(function(){if(!dead&&R)addBalloon();},3500);
}

/* ───────── candles ───────── */
function blow(k,quiet){var w=wedges[k];if(!w||!w.userData.flame||!lit[k])return;lit[k]=false;w.userData.flame.visible=false;puff(w.userData.flame);if(!quiet)sPuff();updateHud();checkOut();}
function relight(k){var w=wedges[k];if(!w||!w.userData.flame||lit[k])return;lit[k]=true;w.userData.flame.visible=true;tone(880,.08,'square',.05);updateHud();wishShown=false;}
function toggleCandle(k){ensureAudio();if(lit[k])blow(k);else relight(k);}
function blowAll(){var ks=candKs().filter(function(k){return lit[k];});if(!ks.length){msg('Saari candles pehle se bujhi hui hain 💨','info');return;}ensureAudio();sPuff();ks.forEach(function(k,n){setTimeout(function(){blow(k,true);},n*120);});}
function relightAll(){candKs().forEach(function(k,n){setTimeout(function(){relight(k);},n*70);});wishShown=false;}
function checkOut(){
 if(litN()===0&&candKs().length>0&&!wishShown){
  wishShown=true;sCheer();msg('🌟 Wish maang li gayi! Denz ke liye dher saari duain 💖','ok');
  burst(0,2.8,0,140);floatText('🌟 Make a Wish, '+(S.name||'Denz')+'! 🌟',stage.clientWidth/2,stage.clientHeight*.28,'font-size:22px;',2400);
  if(dark)setTimeout(function(){setDark(false);},900);
 }
}
function setDark(v){dark=v;var l=$('cake-lbl-lights');if(l)l.textContent=dark?'Lights On':'Lights Off';dimEl.style.opacity=dark?'.92':'0';}
async function micBlow(){
 if(!navigator.mediaDevices||!navigator.mediaDevices.getUserMedia){msg('Is device par mic support nahi — candles par tap karein 👆','err');return;}
 if(litN()===0){msg('Pehle candles jalayein 🔥','info');return;}
 ensureAudio();var st;
 try{st=await navigator.mediaDevices.getUserMedia({audio:true});}catch(e){msg('Mic ki ijazat nahi mili — candles par tap karein 👆','err');return;}
 var AC=window.AudioContext||window.webkitAudioContext,ac=new AC(),src=ac.createMediaStreamSource(st),an=ac.createAnalyser();
 an.fftSize=512;src.connect(an);var buf=new Uint8Array(an.fftSize),hot=0,t0=performance.now(),lb=$('cake-lbl-mic'),iv;
 if(lb)lb.textContent='Sun raha hoon… phoonk maren!';msg('🎤 Mic ki taraf zor se phoonk maren 💨','info');
 micStop=function(){clearInterval(iv);try{st.getTracks().forEach(function(t){t.stop();});ac.close();}catch(e){}if(lb)lb.textContent='Phoonk Maro';micStop=null;};
 iv=setInterval(function(){
  an.getByteTimeDomainData(buf);var s=0,i;for(i=0;i<buf.length;i++){var v=(buf[i]-128)/128;s+=v*v;}
  var rms=Math.sqrt(s/buf.length);
  if(rms>.16){hot++;if(hot>=2){var ks=candKs().filter(function(k){return lit[k];}),n=1+Math.floor(rms*6);for(i=0;i<n&&ks.length;i++)blow(ks.splice(Math.floor(Math.random()*ks.length),1)[0]);}}else hot=0;
  if(performance.now()-t0>25000||litN()===0){if(micStop)micStop();}
 },90);
}
function playTune(){
 if(tuneOn)return;ensureAudio();if(!actx)return;tuneOn=true;var beat=.44,when=0;
 TUNE.forEach(function(n){tone(n[0],n[1]*beat*.95,'triangle',.22,when);tone(n[0]*2,n[1]*beat*.6,'sine',.05,when);when+=n[1]*beat;});
 msg('🎶 Happy Birthday to you… Happy Birthday '+(S.name||'Denz')+'! 🎶','ok');
 setTimeout(function(){tuneOn=false;burst(0,2.6,0,120);sCheer();},when*1000+150);
}

/* ───────── input ───────── */
function pick(e){
 var r=R.domElement.getBoundingClientRect();
 mv.set((e.clientX-r.left)/r.width*2-1,-((e.clientY-r.top)/r.height)*2+1);ray.setFromCamera(mv,cam);
 var hs=ray.intersectObjects([cakeG].concat(balloons),true);
 for(var i=0;i<hs.length;i++){
  var o=hs[i].object,u=o.userData||{};
  if(u.balloon){popBalloon(o);return;}
  if(o.parent&&o.parent.userData&&o.parent.userData.balloon){popBalloon(o.parent);return;}
  if(u.hit){if(!busy)toggleCandle(u.k);return;}
  return;
 }
}
function bindInput(){
 var cv=R.domElement,sx=0,sy=0,t0=0,moved=false,lx=0,ly=0,lt=0;
 cv.addEventListener('pointerdown',function(e){ensureAudio();drag={id:e.pointerId};moved=false;sx=lx=e.clientX;sy=ly=e.clientY;t0=lt=performance.now();vel=0;try{cv.setPointerCapture(e.pointerId);}catch(_){}});
 cv.addEventListener('pointermove',function(e){
  if(!drag)return;
  if(!moved&&Math.hypot(e.clientX-sx,e.clientY-sy)>7)moved=true;
  if(!moved||busy)return;
  var now=performance.now(),dt=Math.max(.008,(now-lt)/1000),dx=e.clientX-lx;lt=now;
  cakeG.rotation.y+=dx*.011;vel=Math.max(-6,Math.min(6,vel*.6+(dx*.011/dt)*.4));
  if(e.pointerType==='mouse')pitchG=Math.max(.12,Math.min(1.05,pitchG+(e.clientY-ly)*.006));
  lx=e.clientX;ly=e.clientY;
 });
 cv.addEventListener('pointerup',function(e){var d=drag;drag=null;try{cv.releasePointerCapture(e.pointerId);}catch(_){}if(d&&!moved&&performance.now()-t0<500)pick(e);});
 cv.addEventListener('pointercancel',function(){drag=null;vel=0;});
}
function onResize(){
 if(!R)return;var w=stage.clientWidth,h=stage.clientHeight;if(!w||!h)return;
 R.setSize(w,h,false);cam.aspect=w/h;cam.updateProjectionMatrix();
}

/* ───────── render loop ───────── */
function frame(now){
 if(dead)return;
 if(!document.body.contains(stage)){window.__cakeClean();return;}
 raf=requestAnimationFrame(frame);
 var dt=Math.min(.05,(now-last)/1000),t=now/1000,i,k;last=now;
 if(!drag&&!busy){cakeG.rotation.y+=vel*dt;vel*=Math.pow(.04,dt);if(autoOn&&Math.abs(vel)<.25)cakeG.rotation.y+=.28*dt;}
 var k1=1-Math.pow(.002,dt);
 pitch+=(pitchG-pitch)*k1;zoom+=(zoomG-zoom)*k1;tgt.lerp(tgtG,k1);
 var a=stage.clientWidth/Math.max(1,stage.clientHeight),d=Math.max(7.6,7.4/a)*zoom;
 cam.position.set(tgt.x,tgt.y+Math.sin(pitch)*d,tgt.z+Math.cos(pitch)*d);cam.lookAt(tgt);

 var n=0;
 for(k=0;k<SECT;k++){var w=wedges[k];if(w&&w.userData.flame&&lit[k]){n++;var s=1+.1*Math.sin(t*12+k*1.7)+(tuneOn?.15*Math.sin(t*5):0);w.userData.flame.scale.set(.34*s,.52*(1+.14*Math.sin(t*8+k)),1);}}
 warm.intensity+=((dark?.5+.3*n:.3+.04*n)-warm.intensity)*k1;
 amb.intensity+=((dark?.12:.75)-amb.intensity)*k1;
 sun.intensity+=((dark?.06:.85)-sun.intensity)*k1;
 banner.position.y=4.3+Math.sin(t*1.2)*.09;
 sparkles.rotation.y=t*.05;sparkles.material.opacity=.6+.4*Math.sin(t*2);
 for(i=0;i<balloons.length;i++){var b=balloons[i],u=b.userData;b.position.set(u.bx+Math.sin(t*.6+u.ph)*.25,u.by+Math.sin(t*.9+u.ph)*.22,u.bz);b.rotation.z=Math.sin(t*.7+u.ph)*.08;}
 var act=false;
 for(i=0;i<CN;i++){var o=i*3;if(cpos[o+1]>-50){act=true;cvel[o+1]-=9*dt;cpos[o]+=cvel[o]*dt;cpos[o+1]+=cvel[o+1]*dt;cpos[o+2]+=cvel[o+2]*dt;if(cpos[o+1]<-1)cpos[o+1]=-99;}}
 if(act)confGeo.attributes.position.needsUpdate=true;
 for(i=puffs.length-1;i>=0;i--){var p=puffs[i];p.age+=dt;if(p.age<0)continue;p.s.visible=true;if(p.age>1.6){scene.remove(p.s);p.s.material.dispose();puffs.splice(i,1);continue;}p.s.position.y+=dt*.9;p.s.position.x+=p.vx*dt;var sc=.15+p.age*.5;p.s.scale.set(sc,sc,1);p.s.material.opacity=.7*(1-p.age/1.6);}
 R.render(scene,cam);
}

/* ───────── reward + eating sequence ───────── */
function applyReward(j,old){
 S.mine=j.mine;S.total=j.total;S.points=j.points;S.recent=j.recent||S.recent;S.top=j.top||S.top;S.wishes=j.wishes||S.wishes;
 var w=stage.clientWidth,h=stage.clientHeight;
 sDing();coinRain();burst(0,2.4,2.5,160);
 floatText('+10,00,000 ⭐',w/2,h*.45,'font-size:34px;color:#fde047;text-shadow:0 3px 0 #b45309,0 0 22px rgba(250,204,21,.9);',2600);
 tween(old,S.points,1600,function(v){$('cake-pts').textContent=ind(v);});
 $('cake-mine').textContent=ind(S.mine);$('cake-total').textContent=ind(S.total);
 msg('🎉 Mubarak! Aapko 10,00,000 points mil gaye. Agli slice 2 ghante baad milegi 🍰','ok');
 renderLists();
}
async function eatSeq(j){
 busy=true;vel=0;var old=S.points||0,rewarded=false;
 var k=((((j.total||1)-1)%SECT)+SECT)%SECT;
 S.wait=j.wait_sec;S.t0=Date.now();syncCTA();
 try{
  if(!ready||!R){applyReward(j,old);rewarded=true;return;}
  if(missing!==k||!wedges[k]){missing=k;buildCake();}
  var W=wedges[k],u=W.userData,w=stage.clientWidth,h=stage.clientHeight;

  /* 1. turn the cake so the chosen slice faces us, camera comes closer */
  pitchG=.62;zoomG=.82;tgtG.set(0,1.2,.6);
  var tar=-Math.atan2(u.dx,u.dz),cur=cakeG.rotation.y,dl=((tar-cur)%PI2+3*Math.PI)%PI2-Math.PI;
  await tween(0,1,1000,function(v){cakeG.rotation.y=cur+dl*v;});
  if(u.candle){u.candle.forEach(function(c){c.visible=false;});hits=hits.filter(function(x){return x.userData.k!==k;});u.flame=null;updateHud();}

  /* 2. knife: two cuts along the slice edges */
  cakeG.updateMatrixWorld(true);
  var kn=fxEl('🔪','font-size:58px;line-height:1;filter:drop-shadow(0 6px 6px rgba(0,0,0,.5));'),ed=[u.a0,u.a1],q;
  for(q=0;q<2;q++){
   var pt=scr(cakeG.localToWorld(new THREE.Vector3(Math.cos(ed[q])*1.9,1.15,-Math.sin(ed[q])*1.9)));
   sWhoosh();
   if(kn.animate)kn.animate([{transform:'translate('+(pt.x-20)+'px,'+(pt.y-150)+'px) rotate(-18deg)'},{transform:'translate('+(pt.x-20)+'px,'+(pt.y-48)+'px) rotate(-18deg)'}],{duration:420,easing:'ease-in',fill:'forwards'});
   await sleep(440);noise(.12,.3,700);crumbs(pt.x,pt.y,9);await sleep(160);
  }
  kn.remove();

  /* 3. the slice lifts out and lands on its own plate */
  var comR=1.25,plate=new THREE.Mesh(G_PLATE,M_WHITE),hd=new THREE.Group();
  hd.position.set(u.dx*comR,0,u.dz*comR);cakeG.add(hd);
  W.position.set(-u.dx*comR,0,-u.dz*comR);hd.add(W);
  plate.position.set(u.dx*(comR+2.15),.13,u.dz*(comR+2.15));plate.scale.setScalar(.01);cakeG.add(plate);
  pitchG=.75;zoomG=.7;tgtG.set(0,.9,2.2);
  await tween(0,1,1100,function(v){var o=comR+2.15*v;hd.position.set(u.dx*o,.06*v+.9*Math.sin(Math.PI*v),u.dz*o);plate.scale.setScalar(Math.max(.01,Math.min(1,v*1.6)));});

  /* 4. four bites with a fork */
  var say=['Mmm… 😋','Yummy! 🤤','Kya baat hai! 😍','Zabardast! 🤩'],SC=[.74,.5,.27,.001],sc0=1,b;
  for(b=0;b<4;b++){
   cakeG.updateMatrixWorld(true);
   var wp=new THREE.Vector3();hd.getWorldPosition(wp);wp.y+=.6;var pc=scr(wp);
   var fk=fxEl('🍴','font-size:54px;line-height:1;filter:drop-shadow(0 6px 5px rgba(0,0,0,.45));'),endT='translate('+(pc.x-27)+'px,'+(pc.y-50)+'px) rotate(0deg)';
   if(fk.animate)fk.animate([{transform:'translate('+(pc.x-27)+'px,'+(pc.y-170)+'px) rotate(-30deg)',opacity:0},{transform:endT,opacity:1}],{duration:380,easing:'ease-out',fill:'forwards'});
   await sleep(420);
   crumbs(pc.x,pc.y,12);sNom();
   await tween(sc0,SC[b],280,function(v){hd.scale.setScalar(Math.max(.001,v));});sc0=SC[b];
   if(fk.animate)fk.animate([{transform:endT,opacity:1},{transform:'translate('+(w/2-27)+'px,'+(h-10)+'px) rotate(12deg) scale(2.4)',opacity:0}],{duration:520,fill:'forwards'});
   floatText(say[b],w/2,h*.72);
   await sleep(560);fk.remove();
  }
  cakeG.remove(hd);cakeG.remove(plate);wedges[k]=null;missing=k+1;updateHud();

  /* 5. reward + camera back */
  applyReward(j,old);rewarded=true;
  pitchG=.42;zoomG=1;tgtG.set(0,1.5,0);
  await sleep(1400);
  if(missing>=SECT){msg('🎂 Cake '+Math.floor((S.total||0)/SECT)+' khatam! Ab Cake '+(Math.floor((S.total||0)/SECT)+1)+' aa gaya 🍰','info');missing=0;lit=[];buildCake();sCheer();burst(0,2.6,0,220);}
 }catch(err){
  console.error('cake eat error',err);
  if(!rewarded){applyReward(j,old);}
  try{if(ready&&R){missing=(((S.total||0)%SECT)+SECT)%SECT;buildCake();}}catch(e2){}
 }finally{
  busy=false;pitchG=.42;zoomG=1;if(tgtG)tgtG.set(0,1.5,0);syncCTA();
 }
}

/* ───────── htmx hooks (the eat button + wish form are normal hx-post elements, so CSRF just works) ───────── */
function onBefore(e){
 var el=e.detail&&e.detail.elt;if(!el)return;
 if(el.id==='cake-cta'){
  if(busy||!S.open||remaining()>0){e.preventDefault();return;}
  busy=true;pending=true;syncCTA();ensureAudio();msg('🔪 Slice kat rahi hai…','info');
  setTimeout(function(){if(pending){pending=false;busy=false;syncCTA();}},15000);
 }
}
function onAfter(e){
 var el=e.detail&&e.detail.elt;if(!el)return;
 var xhr=e.detail.xhr,j=null;try{j=JSON.parse(xhr.responseText);}catch(_){}
 if(el.id==='cake-cta'){
  pending=false;
  if(j&&j.ok){eatSeq(j);return;}
  busy=false;
  if(j){
   if(j.wait_sec!=null){S.wait=j.wait_sec;S.t0=Date.now();}
   if(j.mine!=null){S.mine=j.mine;S.total=j.total;S.points=j.points;S.recent=j.recent||S.recent;S.top=j.top||S.top;S.wishes=j.wishes||S.wishes;updateStats();renderLists();}
   msg(j.msg||'Abhi slice nahi mil sakti','err');
  }else msg((xhr&&xhr.status===403&&xhr.responseText)?String(xhr.responseText).slice(0,160):'Network ka masla — dobara try karein','err');
  syncCTA();
 }else if(el.id==='cake-wish-form'){
  if(j&&j.ok){
   var inp=$('cake-wish-in');if(inp)inp.value='';
   S.wishes=j.wishes||S.wishes;renderLists();ensureAudio();sCheer();
   floatText('🏮 '+j.wish,stage.clientWidth/2,stage.clientHeight*.6,'font-size:17px;max-width:90%;overflow:hidden;text-overflow:ellipsis;',3200);
   burst(0,3,0,60);msg('🏮 Aap ki wish Denz tak pahunch gayi!','ok');
  }else msg((j&&j.msg)||'Wish nahi ja saki','err');
 }
}
document.body.addEventListener('htmx:beforeRequest',onBefore);
document.body.addEventListener('htmx:afterRequest',onAfter);

/* ───────── controls ───────── */
$('cake-controls').addEventListener('click',function(e){
 var b=e.target.closest('[data-act]');if(!b)return;var a=b.getAttribute('data-act');ensureAudio();
 if(a==='blow')blowAll();
 else if(a==='mic'){if(micStop)micStop();else micBlow();}
 else if(a==='relight'){relightAll();sCheer();}
 else if(a==='lights'){setDark(!dark);}
 else if(a==='spin'){autoOn=!autoOn;$('cake-lbl-spin').textContent='Auto-Ghumao: '+(autoOn?'ON':'OFF');}
 else if(a==='tune')playTune();
 else if(a==='confetti'){burst(0,2.6,1,200);sCheer();}
 else if(a==='balloons'&&R){for(var i=0;i<3;i++)addBalloon();sPuff();}
});
$('cake-flavs').addEventListener('click',function(e){
 var b=e.target.closest('[data-flav]');if(!b||busy)return;palKey=b.getAttribute('data-flav');
 Array.prototype.forEach.call($('cake-flavs').children,function(x){var on=x===b;x.style.borderColor=on?'#ec4899':'rgba(128,128,128,.3)';x.style.background=on?'rgba(236,72,153,.14)':'rgba(128,128,128,.08)';});
 if(ready){buildCake();burst(0,2.6,1,50);}tone(660,.1,'triangle',.1);
});

/* live sync: other people's slices, champions and wishes show up */
timers.push(setInterval(function(){
 if(busy||document.hidden)return;
 fetch('/birthday-cake/state',{credentials:'same-origin',headers:{'Accept':'application/json'}})
  .then(function(r){return r.json();})
  .then(function(st){
   if(!st||!st.ok||busy)return;
   S.open=st.open;S.wait=st.wait_sec;S.t0=Date.now();S.mine=st.mine;S.points=st.points;S.recent=st.recent||[];S.top=st.top||[];S.wishes=st.wishes||[];
   if(st.total!==S.total){var oc=Math.floor((S.total||0)/SECT);S.total=st.total;var nc=Math.floor(S.total/SECT),m=((S.total%SECT)+SECT)%SECT;if(nc>oc)msg('🎂 Cake '+(oc+1)+' khatam! Ab Cake '+(nc+1)+' hai 🍰','info');if(ready&&m!==missing){missing=m;if(m===0)lit=[];buildCake();}}
   updateHud();
   updateStats();renderLists();syncCTA();
  }).catch(function(){});
},8000));
updateStats();renderLists();syncCTA();showPop();updateHud();
timers.push(setInterval(syncCTA,1000));

/* ───────── go ───────── */
updateStats();renderLists();syncCTA();showPop();
if(!S.open)msg('🔒 Cake birthday celebration ke dauran khulti hai','info');
loadThree(function(err){
 if(dead)return;
 var ld=$('cake-load');
 if(err||!init3D()){if(ld)ld.innerHTML='<span>3D load nahi ho saka 😕<br>Phir bhi slice kha kar points le sakte hain 🍰</span>';return;}
 ready=true;buildCake();bindInput();window.addEventListener('resize',onResize);
 if(ld)ld.style.display='none';
 last=performance.now();raf=requestAnimationFrame(frame);
});
})();
`