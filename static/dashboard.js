(() => {
  "use strict";

  const STATES = {
    "ONLINE":     {cls:"state-nominal", msg:"ALL SYSTEMS NOMINAL",       sub:""},
    "DEGRADED":   {cls:"state-caution", msg:"PACKET LOSS DETECTED",      sub:"caution"},
    "OBSTRUCTED": {cls:"state-alarm",   msg:"LINE OF SIGHT OBSTRUCTED",  sub:"off-nominal"},
    "NO SIGNAL":  {cls:"state-alarm",   msg:"SIGNAL LOST — REACQUIRING", sub:"off-nominal"},
    "OFFLINE":    {cls:"state-alarm",   msg:"TELEMETRY LINK DOWN",       sub:"no contact"},
  };

  const $ = id => document.getElementById(id);
  const alarm = $("alarm"), alarmMsg = $("alarm-msg"), alarmSub = $("alarm-sub");
  const conn = $("conn"), connTxt = $("conn-txt");
  const fmt = (v, d=1) => (v==null || isNaN(v)) ? "—" : Number(v).toFixed(d);

  function get(sec){
    sec = Math.max(0, Math.floor(sec||0));
    const d = Math.floor(sec/86400); sec-=d*86400;
    const h = Math.floor(sec/3600);  sec-=h*3600;
    const m = Math.floor(sec/60);    const s = sec-m*60;
    const p = n => String(n).padStart(2,"0");
    return `${String(d).padStart(3,"0")}:${p(h)}:${p(m)}:${p(s)}`;
  }

  function setState(link){
    const st = STATES[link] || STATES["OFFLINE"];
    alarm.className = "alarm-strip " + st.cls;
    alarmMsg.textContent = st.msg;
    alarmSub.textContent = st.sub;
  }

  // ---- Time ranges -------------------------------------------------------
  // `ms: null` means everything in the log. For that case the window start is
  // pinned to the oldest sample the server returns and the span grows as time
  // passes, rather than sliding.
  //
  // The id doubles as the query value, so it has to parse as a Go duration
  // (or be the literal "all").
  const RANGES = [
    {id:"2m",  label:"2m",  ms: 2*60*1000},
    {id:"5m",  label:"5m",  ms: 5*60*1000},
    {id:"15m", label:"15m", ms: 15*60*1000},
    {id:"1h",  label:"1h",  ms: 60*60*1000},
    {id:"6h",  label:"6h",  ms: 6*60*60*1000},
    {id:"24h", label:"24h", ms: 24*60*60*1000},
    {id:"all", label:"All", ms: null},
  ];
  const DEFAULT_RANGE = "15m";

  const view = {
    range:   RANGES.find(r => r.id === DEFAULT_RANGE),
    startMs: null,   // only set for the "all" range
    gapMs:   3000,   // adjacent samples further apart than this break the trace
  };

  function viewStart(now){
    return view.startMs !== null ? view.startMs : now - view.range.ms;
  }

  // Hard ceiling on retained points. Pruning is normally by time (see prune),
  // this only catches the "all" range left running for hours.
  const MAXPTS = 20000;

  const cssCache = {};
  function getCSS(name){
    if(!cssCache[name]) cssCache[name]=getComputedStyle(document.documentElement).getPropertyValue(name);
    return cssCache[name];
  }

  // Canvas needs rgba() for the translucent crosshair, and the trace colours
  // arrive as hex from CSS.
  function withAlpha(hex, a){
    let h = hex.trim().replace("#","");
    if(h.length === 3) h = h.split("").map(c => c+c).join("");
    const n = parseInt(h, 16);
    return `rgba(${(n>>16)&255},${(n>>8)&255},${n&255},${a})`;
  }

  // Pick a gridline step that respects the scope's ideal step (minimum) but
  // scales up through nice human-readable values so we never crowd the axis
  // with more than ~6 divisions.
  const STEP_CASCADE = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000];
  function niceStep(top, ideal){
    for(const s of STEP_CASCADE){
      if(s >= ideal && top/s <= 6) return s;
    }
    return STEP_CASCADE[STEP_CASCADE.length-1];
  }

  function makeScope(opts){
    const cv = $(opts.canvas), ctx = cv.getContext("2d"), rngEl = $(opts.rng);
    // Each point is {t: Date, v: number}. Chronological order is preserved
    // by construction (Publish is single-writer server-side, history arrives
    // sorted, live samples arrive newest-last).
    let hist = [];
    let hoverIdx = -1;
    let W=0, H=0;

    const fmtVal = opts.fmtVal || (v => `${Math.round(v)} ${opts.unit}`);

    // Badge lives inside the scope's own container so it's clipped correctly.
    const badge = document.createElement("div");
    badge.className = "badge";
    cv.parentElement.appendChild(badge);
    badge.style.setProperty("--badge-fg", getCSS(opts.color).trim());

    function resize(){
      const dpr = window.devicePixelRatio || 1;
      const r = cv.getBoundingClientRect();
      W = r.width; H = r.height;
      cv.width = Math.round(W*dpr); cv.height = Math.round(H*dpr);
      ctx.setTransform(dpr,0,0,dpr,0,0);
      draw();
    }

    // Drop points that have scrolled off the left edge. Slicing once beats
    // shift()-ing in a loop, and pruning by time rather than by count means
    // a long range keeps its full history no matter how many live samples
    // arrive on top of it.
    function prune(tStart){
      if(hist.length > 1 && hist[0].t < tStart){
        let i = 0;
        while(i < hist.length && hist[i].t < tStart) i++;
        if(i > 0) i--;              // keep one point off-screen so the trace enters from the edge
        if(i > 0){ hist = hist.slice(i); hoverIdx -= i; }
      }
      if(hist.length > MAXPTS){
        const drop = hist.length - MAXPTS;
        hist = hist.slice(drop); hoverIdx -= drop;
      }
      if(hoverIdx < 0) hoverIdx = -1;
    }

    function draw(){
      ctx.clearRect(0,0,W,H);
      if(!W) return;

      const tEnd   = Date.now();
      const tStart = viewStart(tEnd);
      const span   = Math.max(1, tEnd - tStart);
      const xOf = t => W * (t - tStart) / span;

      prune(tStart);

      // Peak over visible samples only — a spike from the far end of the
      // window shouldn't dictate the scale after it scrolls off.
      let peak = 0, visibleCount = 0;
      for(let i=0;i<hist.length;i++){
        const p = hist[i];
        if(p.t < tStart) continue;
        visibleCount++;
        if(p.v > peak) peak = p.v;
      }
      const top = Math.max(opts.floor, Math.ceil(Math.max(peak,0)*1.25/opts.gran)*opts.gran);
      rngEl.textContent = visibleCount ? `0–${top} ${opts.unit} · ${view.range.label}` : `${view.range.label} span`;
      const plotH = H - 14;
      const trace = getCSS(opts.color).trim();
      const yOf = v => plotH * (1 - Math.min(v,top)/top);

      // Grid
      ctx.strokeStyle = getCSS("--grid"); ctx.lineWidth = 1;
      ctx.font = "10px " + getCSS("--mono").trim();
      ctx.fillStyle = getCSS("--label-dim");
      const step = opts.step ? niceStep(top, opts.step) : top/4;
      const divisions = Math.max(1, Math.round(top/step));
      for(let i=0;i<=divisions;i++){
        const y = Math.round(plotH*i/divisions) + 0.5;
        ctx.beginPath(); ctx.moveTo(0,y); ctx.lineTo(W,y); ctx.stroke();
        const val = Math.round(top - i*step);
        ctx.fillText(String(val), 2, (y-3<8) ? y+11 : y-3);
      }
      if(visibleCount < 1) return;

      // Split visible samples into contiguous segments, breaking anywhere
      // consecutive readings are more than view.gapMs apart. Each segment
      // gets its own fill + stroke so offline periods render as blank space.
      // The threshold tracks the server's bucket width — at 24h the points
      // are ~40s apart by design, and a fixed 3s gap would shred the trace.
      const segments = [];
      let cur = [];
      for(let i=0;i<hist.length;i++){
        const p = hist[i];
        if(p.t < tStart) continue;
        if(cur.length > 0 && p.t - cur[cur.length-1].t > view.gapMs){
          segments.push(cur); cur = [];
        }
        cur.push(p);
      }
      if(cur.length > 0) segments.push(cur);

      for(const seg of segments){
        // Fill under
        ctx.beginPath();
        ctx.moveTo(xOf(seg[0].t), plotH);
        for(const p of seg) ctx.lineTo(xOf(p.t), yOf(p.v));
        ctx.lineTo(xOf(seg[seg.length-1].t), plotH);
        ctx.closePath();
        // Trace
        ctx.beginPath();
        for(let j=0;j<seg.length;j++){
          const x = xOf(seg[j].t), y = yOf(seg[j].v);
          j ? ctx.lineTo(x,y) : ctx.moveTo(x,y);
        }
        ctx.strokeStyle = trace; ctx.lineWidth = 1; ctx.lineJoin = "round"; ctx.stroke();
      }

      // Leading dot on the most recent visible sample
      const last = hist[hist.length-1];
      if(last && last.t >= tStart){
        const lx = xOf(last.t), ly = yOf(last.v);
        ctx.beginPath(); ctx.arc(lx,ly,2.6,0,Math.PI*2); ctx.fillStyle=trace; ctx.fill();
      }

      // Hover crosshair
      if(hoverIdx >= 0 && hoverIdx < hist.length){
        const p = hist[hoverIdx];
        if(p.t >= tStart){
          const hx = xOf(p.t), hy = yOf(p.v);
          ctx.strokeStyle = "rgba(245,183,64,.35)"; ctx.lineWidth = 1;
          ctx.beginPath(); ctx.moveTo(Math.round(hx)+0.5, 0); ctx.lineTo(Math.round(hx)+0.5, plotH); ctx.stroke();
          ctx.beginPath(); ctx.arc(hx, hy, 3.2, 0, Math.PI*2); ctx.fillStyle = trace; ctx.fill();
          ctx.beginPath(); ctx.arc(hx, hy, 5.5, 0, Math.PI*2);
          ctx.strokeStyle = "rgba(245,183,64,.45)"; ctx.lineWidth = 1; ctx.stroke();
        }
      }
    }

    // pushSilent: append without redrawing. Used when loading a range so we
    // can insert a couple thousand points without repainting once per point.
    // Caller must invoke draw() when the batch is done.
    function pushSilent(t, v){
      hist.push({t, v});
    }
    function push(t, v){
      pushSilent(t, v);
      if(hoverIdx >= 0 && lastMouseX !== null) hoverIdx = idxAtX(lastMouseX);
      draw();
      if(hoverIdx >= 0) updateBadge();
    }
    function clear(){
      hist = [];
      hoverIdx = -1;
      badge.classList.remove("on");
    }

    // Binary search for the sample closest in TIME to the pixel x. Then
    // gate on pixel distance so hovering in the middle of a big gap doesn't
    // snap onto a distant sample from the far side of the gap.
    function idxAtX(px){
      if(hist.length < 1) return -1;
      const tEnd   = Date.now();
      const tStart = viewStart(tEnd);
      const span   = Math.max(1, tEnd - tStart);
      const tTarget = tStart + (px/W) * span;

      let lo = 0, hi = hist.length - 1;
      while(lo < hi){
        const mid = (lo + hi) >> 1;
        if(hist[mid].t < tTarget) lo = mid + 1; else hi = mid;
      }
      let idx = lo;
      if(lo > 0 && Math.abs(hist[lo-1].t - tTarget) < Math.abs(hist[lo].t - tTarget)){
        idx = lo - 1;
      }
      if(hist[idx].t < tStart) return -1;
      // Snap only if within ~8px of the nearest sample.
      const sampleX = W * (hist[idx].t - tStart) / span;
      if(Math.abs(sampleX - px) > 8) return -1;
      return idx;
    }

    function updateBadge(){
      if(hoverIdx < 0 || hoverIdx >= hist.length){ badge.classList.remove("on"); return; }
      const p = hist[hoverIdx];
      const tEnd   = Date.now();
      const tStart = viewStart(tEnd);
      const span   = Math.max(1, tEnd - tStart);
      const hx = W * (p.t - tStart) / span;

      const bx = cv.offsetLeft + hx;
      const by = cv.offsetTop;
      badge.style.left = bx + "px";
      badge.style.top  = by + "px";

      const ts = p.t;
      const hh = String(ts.getHours()).padStart(2,"0");
      const mm = String(ts.getMinutes()).padStart(2,"0");
      const ss = String(ts.getSeconds()).padStart(2,"0");
      badge.innerHTML = `${fmtVal(p.v)}<span class="t">${hh}:${mm}:${ss}</span>`;
      badge.classList.add("on");
    }

    let lastMouseX = null;
    cv.addEventListener("mousemove", e => {
      const r = cv.getBoundingClientRect();
      lastMouseX = e.clientX - r.left;
      const newIdx = idxAtX(lastMouseX);
      if(newIdx !== hoverIdx){ hoverIdx = newIdx; draw(); }
      updateBadge();
    });
    cv.addEventListener("mouseleave", () => {
      lastMouseX = null;
      hoverIdx = -1;
      badge.classList.remove("on");
      draw();
    });

    return {resize, draw, push, pushSilent, clear};
  }

  const fmtMs   = v => `${Math.round(v)} ms`;
  const fmtPct  = v => `${v.toFixed(2)} %`;
  const fmtMbps = v => `${v.toFixed(1)} Mbps`;

  const latScope  = makeScope({canvas:"sc-lat",  rng:"rng-lat",  unit:"ms",   floor:60,  gran:20,  color:"--sc-lat",  fmtVal: fmtMs});
  const lossScope = makeScope({canvas:"sc-loss", rng:"rng-loss", unit:"%",    floor:5,   gran:5,   step:1, color:"--sc-loss", fmtVal: fmtPct});
  const downScope = makeScope({canvas:"sc-down", rng:"rng-down", unit:"Mbps", floor:100, gran:100, color:"--sc-down", fmtVal: fmtMbps});
  const upScope   = makeScope({canvas:"sc-up",   rng:"rng-up",   unit:"Mbps", floor:20,  gran:20,  color:"--sc-up",   fmtVal: fmtMbps});
  const scopes = [latScope, lossScope, downScope, upScope];

  // ---- Applying samples --------------------------------------------------
  // updateReadouts sets the current-state UI (alarm strip, tiles, GET clock).
  // chartSample only pushes to the strip charts. Splitting the two lets a
  // range load push thousands of points silently without doing thousands of
  // pointless UI updates — and keeps decimated worst-case buckets out of the
  // readouts, which must always show the genuine latest reading.
  function updateReadouts(s){
    setState(s.link);
    $("v-lat").textContent  = fmt(s.latency_ms,0);
    $("v-down").textContent = fmt(s.downlink_mbps,1);
    $("v-up").textContent   = fmt(s.uplink_mbps,1);
    const dropPct = (s.drop_rate||0)*100;
    $("v-drop").textContent = fmt(dropPct,1);

    $("c-lat").className  = "cell" + (s.latency_ms>100 ? " hot" : s.latency_ms>70 ? " warm" : "");
    $("c-drop").className = "cell" + (dropPct>5 ? " hot" : dropPct>0.5 ? " warm" : "");

    const offline = s.link === "OFFLINE";
    $("v-obs-state").textContent = offline ? "—" : (s.obstructed ? "OBSTRUCTED" : "CLEAR");
    $("v-obs").className = "v row " + (offline ? "" : s.obstructed ? "alarm" : "go");
    $("v-obs-pct").textContent = offline ? "" : fmt((s.obstruction_fraction||0)*100, 2) + "% obstruction";

    if(s.hardware_version) $("v-hw").textContent = s.hardware_version;
    if(s.software_version) $("v-sw").textContent = s.software_version;
    $("get").textContent = get(s.uptime_seconds);
    $("v-seen").textContent = s.timestamp
      ? new Date(s.timestamp).toLocaleTimeString()
      : new Date().toLocaleTimeString();
  }

  function chartSample(s, silent){
    if(s.link === "OFFLINE") return;
    const t = s.timestamp ? new Date(s.timestamp) : new Date();
    const dropPct = (s.drop_rate||0)*100;
    const method = silent ? "pushSilent" : "push";
    lossScope[method](t, dropPct);
    downScope[method](t, s.downlink_mbps||0);
    upScope[method](t, s.uplink_mbps||0);
    if(s.latency_ms>0) latScope[method](t, s.latency_ms);
  }

  function applyLive(s){ updateReadouts(s); chartSample(s, false); }

  // ---- Range selector ----------------------------------------------------
  const rangeBar = $("ranges");

  function buildRangeBar(){
    for(const r of RANGES){
      const b = document.createElement("button");
      b.type = "button";
      b.className = "range";
      b.dataset.id = r.id;
      b.textContent = r.label;
      b.addEventListener("click", () => selectRange(r.id));
      rangeBar.appendChild(b);
    }
  }

  function markActive(id){
    for(const b of rangeBar.children){
      const on = b.dataset.id === id;
      b.classList.toggle("on", on);
      b.setAttribute("aria-pressed", String(on));
    }
  }

  // Guards against out-of-order responses: mash 24h then 2m and the slow 24h
  // reply must not overwrite the fast one.
  let loadToken = 0;

  async function selectRange(id){
    const r = RANGES.find(x => x.id === id);
    if(!r) return;
    const token = ++loadToken;

    view.range = r;
    view.startMs = null;
    markActive(r.id);
    rangeBar.classList.add("loading");

    try{
      const res = await fetch(`/history?range=${encodeURIComponent(r.id)}`, {cache:"no-store"});
      if(!res.ok) throw new Error(`${res.status} ${res.statusText}`);
      const data = await res.json();
      if(token !== loadToken) return; // superseded by a later click

      const pts = Array.isArray(data.samples) ? data.samples : [];

      // Trace-breaking threshold follows the server's bucket width, with a
      // 2.5x allowance for jitter and the occasional dropped poll.
      view.gapMs = Math.max(3000, (data.bucket_ms || 1000) * 2.5);

      if(r.ms === null){
        view.startMs = pts.length
          ? new Date(pts[0].timestamp).getTime()
          : Date.now() - 60*1000;
      }

      scopes.forEach(sc => sc.clear());
      for(const s of pts) chartSample(s, true);
      scopes.forEach(sc => sc.draw());
    }catch(err){
      if(token === loadToken) console.error("could not load range", r.id, err);
    }finally{
      if(token === loadToken) rangeBar.classList.remove("loading");
    }
  }

  // ---- Live stream -------------------------------------------------------
  function connect(){
    const es = new EventSource("/events");
    es.onopen  = () => { conn.className="conn live"; connTxt.textContent="Telemetry live"; };
    // The hub's backfill is no longer the charts' data source — /history owns
    // that — so we only take the newest sample from it to prime the readouts.
    es.addEventListener("backfill", e => {
      try{
        const arr = JSON.parse(e.data);
        if(Array.isArray(arr) && arr.length) updateReadouts(arr[arr.length-1]);
      }catch(_){}
    });
    es.onmessage = e => { try{ applyLive(JSON.parse(e.data)); }catch(_){} };
    es.onerror   = () => { conn.className="conn lost"; connTxt.textContent="Reacquiring…"; setState("OFFLINE"); };
  }

  // Repaint once per second even without new data, so the strip keeps
  // advancing to keep "now" at the right edge — otherwise a disconnected
  // client would show a frozen chart with the last sample stuck on the right.
  setInterval(() => scopes.forEach(sc => sc.draw()), 1000);

  // On a long range, live 1 Hz samples pile onto a decimated series and the
  // right-hand end slowly gets denser than the rest. Re-pulling occasionally
  // re-buckets it. Harmless to delete if the reload flicker bothers you.
  setInterval(() => {
    if(view.range.ms === null || view.range.ms > 15*60*1000) selectRange(view.range.id);
  }, 5*60*1000);

  window.addEventListener("resize", () => scopes.forEach(s => s.resize()));
  buildRangeBar();
  scopes.forEach(s => s.resize());
  connect();
  selectRange(DEFAULT_RANGE);
})();
