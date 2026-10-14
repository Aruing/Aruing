/* ═══════════════════════════════════════════════════════════════
   Aruing site — the living-cluster engine, 3D edition.
   Vanilla Canvas 2D with a hand-rolled perspective projection.
   No dependencies. Respects prefers-reduced-motion & visibility.

   Scene semantics (every motion maps to an ops meaning):
     nodes        = pods / containers        (orbs of light, breathing)
     groups       = services / deployments   (puffs in a spherical swarm)
     the camera sits INSIDE the swarm — orbit = looking around from within
     satellites   = replicas orbiting a hub
     dust         = ambient telemetry noise
     streams      = background synapses firing
     pathogen     = injected fault;  mutation = internal fault
     gold comet   = aruing agent; its lit path = traceable chain
     waves        = anomaly shockwaves;  heartbeat = cluster liveness
     memory tint  = a healed cell keeps a whisper of gold (immune memory)
   ═══════════════════════════════════════════════════════════════ */
(function () {
  'use strict';

  var TAU = Math.PI * 2;
  var FOV = 520;          // perspective field of view (px)
  var CAM_DIST = 240;     // camera distance behind the z=0 plane (× zoom)

  function clamp(v, a, b) { return v < a ? a : (v > b ? b : v); }
  function rand(a, b) { return a + Math.random() * (b - a); }
  function randInt(a, b) { return Math.floor(rand(a, b + 1)); }
  function dist3(ax, ay, az, bx, by, bz) {
    var dx = ax - bx, dy = ay - by, dz = az - bz;
    return Math.sqrt(dx * dx + dy * dy + dz * dz);
  }
  function easeOutCubic(t) { return 1 - Math.pow(1 - t, 3); }
  function easeOutBack(t) { var c1 = 1.70158, c3 = c1 + 1; return 1 + c3 * Math.pow(t - 1, 3) + c1 * Math.pow(t - 1, 2); }

  function mixWhite(col, t) {
    var p = col.split(',');
    return Math.round(+p[0] + (255 - +p[0]) * t) + ', ' +
           Math.round(+p[1] + (255 - +p[1]) * t) + ', ' +
           Math.round(+p[2] + (255 - +p[2]) * t);
  }
  function mixCol(a, b, t) {
    var p = a.split(','), q = b.split(',');
    return Math.round(+p[0] + (+q[0] - +p[0]) * t) + ', ' +
           Math.round(+p[1] + (+q[1] - +p[1]) * t) + ', ' +
           Math.round(+p[2] + (+q[2] - +p[2]) * t);
  }
  function wrapPi(a) {
    a = a % TAU;
    if (a > Math.PI) a -= TAU;
    if (a < -Math.PI) a += TAU;
    return a;
  }

  /* service metadata pools for hover tooltips */
  var SVC_POOL = ['demo-api', 'worker-batch', 'ingress-gw', 'redis-cache', 'pg-primary',
    'queue-svc', 'auth-svc', 'metrics-agt', 'search-idx', 'billing-api',
    'notify-svc', 'cdn-origin', 'graph-query', 'ml-inference'];
  var NS_POOL = ['demo', 'prod', 'infra', 'data'];
  var AGE_POOL = ['2d', '3d', '6h', '11d', '19h', '47m', '5d'];
  function podHash() {
    var s = '', cs = 'abcdefghijklmnopqrstuvwxyz0123456789';
    for (var i = 0; i < 5; i++) s += cs.charAt(randInt(0, cs.length - 1));
    return s;
  }

  function ClusterEngine(canvas, opts) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.opts = opts || {};
    this.hooks = this.opts.hooks || {};

    this.w = 0; this.h = 0; this.dpr = 1;
    this.cells = []; this.edges = []; this.adj = []; this.edgeBy = {};
    this.groups = [];           // {x,y,z, ux,uy,uz, vx,vy,vz} tissue plane basis
    this.cellOrder = [];
    this.dust = [];             // ambient motes
    this.streams = [];          // background synapses
    this.waves = [];            // anomaly shockwaves
    this.pulses = [];
    this.particles = [];
    this.heartbeats = [];
    this.litPath = [];
    this.agent = null;
    this.incidents = [];         // 1–3 concurrent anomalies; agent works the queue
    this.activeTarget = null;    // incident the agent is currently on
    this.incidentSeq = 0;
    this.nextIncidentAt = Infinity;
    this.genesis = null;
    this.ready = false;
    this.calm = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    // stage 1 — dark stage: nothing renders until genesis starts (terminal only)
    this.awaitingGenesis = !this.calm;
    // viewer camera: drag to orbit, wheel to zoom (no hover reaction);
    // after 3s idle it softly tracks the agent so it never leaves the frame
    this.cam = { yaw: 0, pitch: 0, zoom: 1, tYaw: 0, tPitch: 0, tZoom: 1, lastInput: -1e9, idleT: 0, followT: 0, visT: null, fx: null, fy: null };
    this.agentScreen = null;
    this.time = 0;
    this.last = 0;
    this.running = false;
    this.raf = 0;
    this.pulseTimer = 0;
    this.hbTimer = 4;
    this.streamTimer = 3;
    this.auroraIn = 0;
    this.px = null; this.py = null;   // pointer for hover hit-testing only
    this.hoverIdx = -1;
    this.hoverEnabled = true;
    this.yaw = 0; this.pitch = 0; this.cy = 0; this.cyw = 0;
    this.frame = this.frame.bind(this);

    var self = this;
    this._onMove = function (e) { self.px = e.clientX; self.py = e.clientY; };
    this._onLeave = function () { self.px = null; self.py = null; };
    this._onVis = function () {
      if (document.hidden) { self.stop(); }
      else { self.start(); }
    };
    this._onResize = function () {
      clearTimeout(self._rsT);
      self._rsT = setTimeout(function () { self.onResize(); }, 160);
    };
    window.addEventListener('mousemove', this._onMove, { passive: true });
    document.addEventListener('mouseleave', this._onLeave);
    document.addEventListener('visibilitychange', this._onVis);
    window.addEventListener('resize', this._onResize);

    this.resize();
    this.build();
    if (this.calm) {
      this.ready = true;
      this.nextIncidentAt = Infinity;
    }
  }

  /* ── camera / projection ────────────────────────────────── */

  /* true screen-space occlusion: a foreground cell disk covers the agent head */
  ClusterEngine.prototype.agentOccluded = function () {
    var s = this.agentScreen;
    if (!s || !s.ok) return false;
    var cells = this.cells;
    for (var i = 0; i < cells.length; i++) {
      var c = cells[i];
      if (c.sx == null || c.born < 0.9) continue;
      if (c.sz > s.z - 30) continue;               // only cells in front of the agent
      var dx = c.sx - s.x, dy = c.sy - s.y;
      var rr = c.r * c.sd * 1.15 + 4;
      if (dx * dx + dy * dy < rr * rr) return true; // disk covers the head
    }
    return false;
  };

  ClusterEngine.prototype.updateCamera = function (dt) {
    var cam = this.cam;
    var idleFor = this.time - cam.lastInput;

    // ── follow: after 3s without input, the camera pans with the agent ──
    // hovering a node freezes the camera — a tooltip needs a steady frame;
    // follow ramps back in after the hover ends
    var hovering = this.hoverEnabled && this.px != null && this.hoverIdx >= 0;
    var canFollow = this.ready && !this.calm && !this.awaitingGenesis &&
                    this.agent && this.agent.visible;
    cam.followT = clamp(cam.followT + (canFollow && idleFor > 3 && !hovering ? dt * 0.8 : -dt * 4), 0, 1);
    if (cam.followT > 0.01 && canFollow) {
      var ag = this.agent;
      var scr = this.agentScreen;
      var visible = !!(scr && scr.ok);
      var occluded = visible ? this.agentOccluded() : false;
      // continuous visibility factor — no binary flapping at occluder edges
      var vTarget = visible && !occluded ? 1 : 0;
      cam.visT = cam.visT == null ? vTarget : cam.visT + (vTarget - cam.visT) * (1 - Math.exp(-dt * 3));
      // framing anchor, heavily smoothed: the agent's local orbit must not
      // shake the camera — only sustained travel moves the frame
      var fxT = visible ? clamp(scr.x, this.w * 0.24, this.w * 0.76) : this.cyw;
      var fyT = visible ? clamp(scr.y, this.h * 0.22, this.h * 0.72) : this.cy;
      if (cam.fx == null) { cam.fx = fxT; cam.fy = fyT; }
      var sm = 1 - Math.exp(-dt * 0.9);
      cam.fx += (fxT - cam.fx) * sm;
      cam.fy += (fyT - cam.fy) * sm;
      var tx = this.cyw + (cam.fx - this.cyw) * cam.visT;
      var ty = this.cy + (cam.fy - this.cy) * cam.visT;
      var alen = Math.sqrt(ag.x * ag.x + ag.y * ag.y + ag.z * ag.z);
      var cd = FOV + CAM_DIST * cam.zoom;
      var K = Math.max(100, alen * cd / (alen + cd)); // px of screen per radian
      // angles that would center the agent, then offset to the framed target
      var yawStar = Math.atan2(ag.x, ag.z);
      var pitchStar = Math.atan2(ag.y, Math.sqrt(ag.x * ag.x + ag.z * ag.z));
      var dYaw = wrapPi(yawStar - (tx - this.cyw) / K - cam.tYaw);
      var dPitch = clamp(pitchStar - (ty - this.cy) / K, -1.0, 1.0) - cam.tPitch;
      // near the view axis the yaw target is ill-defined — ease authority there
      var axis = clamp(Math.sqrt(ag.x * ag.x + ag.z * ag.z) / 220, 0.15, 1);
      var fk = (1 - Math.exp(-dt * 2.0 * cam.followT)) * axis;
      cam.tYaw += dYaw * fk;
      cam.tPitch = clamp(cam.tPitch + dPitch * fk, -1.15, 1.15);
    } else {
      cam.fx = null; cam.fy = null; cam.visT = null;
      // idle auto-rotation when there is nothing to follow (never while hovering)
      var idle = idleFor > 4 && !this.calm && !this.awaitingGenesis && !hovering;
      cam.idleT = clamp(cam.idleT + (idle ? dt : -dt * 3), 0, 2);
      if (cam.idleT > 0) cam.tYaw += dt * 0.05 * (cam.idleT / 2);
    }

    var k = 1 - Math.exp(-dt * 8);
    cam.yaw += (cam.tYaw - cam.yaw) * k;
    cam.pitch += (cam.tPitch - cam.pitch) * k;
    cam.zoom += (cam.tZoom - cam.zoom) * k;
    this.yaw = cam.yaw;
    this.pitch = cam.pitch;
    this.cy = this.h * 0.46;
    this.cyw = this.w * 0.5;
  };

  /* viewer input: drag to orbit, wheel to zoom, double-click to reset */
  ClusterEngine.prototype.orbitBy = function (dx, dy) {
    var cam = this.cam;
    cam.lastInput = this.time;
    cam.tYaw += dx * 0.005;
    cam.tPitch = clamp(cam.tPitch + dy * 0.004, -1.15, 1.15);
  };

  ClusterEngine.prototype.zoomBy = function (f) {
    var cam = this.cam;
    cam.lastInput = this.time;
    cam.tZoom = clamp(cam.tZoom * f, 0.28, 3.2);
  };

  ClusterEngine.prototype.resetView = function () {
    var cam = this.cam;
    cam.lastInput = this.time;
    var rest = cam.tYaw % TAU;      // shortest angular path back
    if (rest > Math.PI) rest -= TAU;
    if (rest < -Math.PI) rest += TAU;
    cam.tYaw -= rest;
    cam.tPitch = 0;
    cam.tZoom = 1;
  };

  ClusterEngine.prototype.project = function (x, y, z) {
    var cd = CAM_DIST * this.cam.zoom;
    var cy = Math.cos(this.yaw), sy = Math.sin(this.yaw);
    var x1 = x * cy - z * sy;
    var z1 = x * sy + z * cy;
    var cp = Math.cos(this.pitch), sp = Math.sin(this.pitch);
    var y1 = y * cp - z1 * sp;
    var z2 = y * sp + z1 * cp;
    var denom = FOV + cd + z2;
    var d = (FOV + cd) / denom;
    // near-plane cull: points behind (or nearly behind) the camera must not draw —
    // a negative scale means negative arc radii, which throw and kill the RAF chain
    var ok = denom > 60;
    return { x: this.cyw + x1 * d, y: this.cy + y1 * d, d: Math.max(0.05, d), z: z2, ok: ok };
  };

  /* ── layout ─────────────────────────────────────────────── */

  ClusterEngine.prototype.resize = function () {
    this.dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.w = window.innerWidth;
    this.h = window.innerHeight;
    this.canvas.width = Math.round(this.w * this.dpr);
    this.canvas.height = Math.round(this.h * this.dpr);
    this.canvas.style.width = this.w + 'px';
    this.canvas.style.height = this.h + 'px';
    this.ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
  };

  ClusterEngine.prototype.screenExcluded = function (x, y, z, pad) {
    // keep-out zone for the hero copy (checked at the rest camera)
    var p = this.project(x, y, z);
    var ex = this.w * 0.5, ey = this.h * 0.44;
    var rx = Math.min(this.w * 0.24, 330) + (pad || 0);
    var ry = 150 + (pad || 0);
    var dx = (p.x - ex) / rx, dy = (p.y - ey) / ry;
    return dx * dx + dy * dy < 1;
  };

  ClusterEngine.prototype.build = function () {
    var w = this.w, h = this.h;
    var cells = this.cells = [];
    var groups = this.groups = [];

    // a spherical swarm the camera sits deep inside — full from every angle,
    // orbiting feels like looking around from within the cluster
    var R = clamp(Math.max(w, h) * 0.72, 700, 1050);
    this.R = R;
    // anisotropic stretch: the stage is wide — an isotropic ball reads as a
    // vertical strip on a 16:9 screen; wide-x / slim-y fills the frame
    var SX = 1.28, SY = 0.92;
    var KEEP_OUT = 170;   // keep the core around the camera axis empty
    var nGroups = clamp(Math.round((w * h) / 60000), 10, 22);
    var minDist = Math.min(w, h) * 0.10;
    var puffR = 70;

    this.yaw = 0; this.pitch = 0; // layout against the rest camera

    for (var g = 0; g < nGroups; g++) {
      var cx = 0, cy = 0, cz = 0, ok = false;
      for (var att = 0; att < 70 && !ok; att++) {
        // volume-weighted point in the ball, outside the keep-out core
        var dxx = rand(-1, 1), dyy = rand(-1, 1), dzz = rand(-1, 1);
        var dl = Math.sqrt(dxx * dxx + dyy * dyy + dzz * dzz) || 1;
        var grr = KEEP_OUT + (R - KEEP_OUT - puffR) * Math.cbrt(Math.random());
        cx = (dxx / dl) * grr;
        cy = (dyy / dl) * grr;
        cz = (dzz / dl) * grr;
        if (this.screenExcluded(cx, cy, cz, 40)) continue;
        ok = true;
        for (var k = 0; k < groups.length; k++) {
          var G = groups[k];
          if (dist3(cx, cy, cz, G.x, G.y, G.z) < minDist) { ok = false; break; }
        }
        if (!ok && att > 34) minDist *= 0.94;
      }
      if (!ok) continue;
      // random orientation basis (used by waves & hub satellites)
      var nx = rand(-1, 1), ny = rand(-1, 1), nz = rand(-1, 1);
      var nl = Math.sqrt(nx * nx + ny * ny + nz * nz) || 1;
      nx /= nl; ny /= nl; nz /= nl;
      var ax = Math.abs(ny) < 0.9 ? 0 : 1, ay = Math.abs(ny) < 0.9 ? 1 : 0;
      var ux = ny * 0 - nz * ay, uy = nz * ax - nx * 0, uz = nx * ay - ny * ax;
      var ul = Math.sqrt(ux * ux + uy * uy + uz * uz) || 1;
      ux /= ul; uy /= ul; uz /= ul;
      var vx = ny * uz - nz * uy, vy = nz * ux - nx * uz, vz = nx * uy - ny * ux;
      groups.push({ x: cx, y: cy, z: cz, ux: ux, uy: uy, uz: uz, vx: vx, vy: vy, vz: vz });
    }

    // cells: a volumetric puff around each group center — nodes fill 3D space
    for (var gi = 0; gi < groups.length; gi++) {
      var grp = groups[gi];
      var n = randInt(7, 13);
      var spacing = rand(34, 50);
      for (var i = 0; i < n; i++) {
        var px, py, pz;
        if (i === 0) {
          px = grp.x; py = grp.y; pz = grp.z;
        } else {
          // random direction on the sphere, volume-weighted radius
          var dxx = rand(-1, 1), dyy = rand(-1, 1), dzz = rand(-1, 1);
          var dl = Math.sqrt(dxx * dxx + dyy * dyy + dzz * dzz) || 1;
          var rad = spacing * (0.45 + 0.85 * Math.pow(Math.random(), 0.6));
          px = grp.x + (dxx / dl) * rad;
          py = grp.y + (dyy / dl) * rad;
          pz = grp.z + (dzz / dl) * rad;
        }
        var prr = Math.sqrt(px * px + py * py + pz * pz);
        if (prr > R || prr < 150) continue;
        if (this.screenExcluded(px, py, pz, 20)) continue;
        var hue = Math.random();
        var col = hue < 0.72
          ? '96, 226, 210'
          : (hue < 0.90 ? '122, 202, 240' : '156, 238, 218');
        cells.push({
          ax: px, ay: py, az: pz,
          x: null, y: null, z: null,           // current world
          sx: null, sy: null, sd: 1, sz: 0,    // projected
          group: gi,
          r: i === 0 ? rand(11.5, 14.5) : (i < 3 ? rand(8, 10.5) : rand(5, 8)),
          sat: i === 0 ? randInt(2, 3) : (i < 3 ? randInt(1, 2) : 0),
          col: col,
          amp: rand(2.4, 5.2), amp2: rand(1.4, 3.0),
          f1: rand(0.10, 0.22), f2: rand(0.07, 0.15), f3: rand(0.10, 0.2), fz: rand(0.06, 0.14),
          ph1: rand(0, TAU), ph2: rand(0, TAU), ph3: rand(0, TAU), phz: rand(0, TAU),
          brF: rand(0.7, 1.3), brPh: rand(0, TAU),
          s1: rand(0, TAU), s2: rand(0, TAU),
          state: 'calm', healT: 0, glow: 0, flicker: 1,
          born: 1, delay: 0, parentIdx: -1,
          dim: rand(0.75, 1),
          pathogen: null, degree: 0, memory: false
        });
      }
    }

    // relax overlaps in 3D
    for (var it = 0; it < 7; it++) {
      for (var a = 0; a < cells.length; a++) {
        var ca = cells[a];
        for (var b = a + 1; b < cells.length; b++) {
          var cb = cells[b];
          var d = dist3(ca.ax, ca.ay, ca.az, cb.ax, cb.ay, cb.az);
          var min = ca.r + cb.r + 16;
          if (d < min && d > 0.001) {
            var push = (min - d) / 2;
            var nx2 = (cb.ax - ca.ax) / d, ny2 = (cb.ay - ca.ay) / d, nz2 = (cb.az - ca.az) / d;
            ca.ax -= nx2 * push; ca.ay -= ny2 * push; ca.az -= nz2 * push;
            cb.ax += nx2 * push; cb.ay += ny2 * push; cb.az += nz2 * push;
          }
        }
        if (this.screenExcluded(ca.ax, ca.ay, ca.az, 8)) {
          var dl = Math.sqrt(ca.ax * ca.ax + ca.ay * ca.ay) || 1;
          ca.ax = (ca.ax / dl) * (this.w * 0.46);
          ca.ay = (ca.ay / dl) * (this.h * 0.42);
        }
      }
    }

    // apply the screen-aspect stretch after relaxation (edges are built after)
    for (var stc = 0; stc < cells.length; stc++) {
      cells[stc].ax *= SX; cells[stc].ay *= SY;
    }
    for (var stg = 0; stg < groups.length; stg++) {
      groups[stg].x *= SX; groups[stg].y *= SY;
    }

    // edges: nearest neighbors through 3D space — a true volumetric plexus
    var edgeSet = {};
    function addEdge(i, j) {
      var key = i < j ? i + '-' + j : j + '-' + i;
      if (edgeSet[key]) return;
      edgeSet[key] = true;
      cells[i].degree++; cells[j].degree++;
    }
    for (var c1 = 0; c1 < cells.length; c1++) {
      var cand = [];
      for (var c2 = 0; c2 < cells.length; c2++) {
        if (c2 === c1) continue;
        cand.push({
          i: c2,
          d: dist3(cells[c1].ax, cells[c1].ay, cells[c1].az, cells[c2].ax, cells[c2].ay, cells[c2].az)
        });
      }
      cand.sort(function (p, q) { return p.d - q.d; });
      var take = 2 + (Math.random() < 0.55 ? 1 : 0) + (Math.random() < 0.18 ? 1 : 0);
      for (var t = 0; t < Math.min(take, cand.length); t++) {
        if (cand[t].d < 200) addEdge(c1, cand[t].i);
      }
    }
    // long-range links between service hubs + a few random cross links
    for (var ga = 0; ga < groups.length; ga++) {
      for (var gb = ga + 1; gb < groups.length; gb++) {
        var best = null;
        for (var ia = 0; ia < cells.length; ia++) {
          if (cells[ia].group !== ga || cells[ia].r < 11) continue;
          for (var ib = 0; ib < cells.length; ib++) {
            if (cells[ib].group !== gb || cells[ib].r < 8) continue;
            var dd = dist3(cells[ia].ax, cells[ia].ay, cells[ia].az, cells[ib].ax, cells[ib].ay, cells[ib].az);
            if (!best || dd < best.d) best = { a: ia, b: ib, d: dd };
          }
        }
        if (best && best.d < Math.min(w, h) * 0.85) addEdge(best.a, best.b);
      }
    }
    for (var xl = 0; xl < 5 && cells.length > 10; xl++) {
      var xi = randInt(0, cells.length - 1), xj = randInt(0, cells.length - 1);
      if (xi === xj) continue;
      var xd = dist3(cells[xi].ax, cells[xi].ay, cells[xi].az, cells[xj].ax, cells[xj].ay, cells[xj].az);
      if (xd < Math.min(w, h) * 0.55) addEdge(xi, xj);
    }

    this.edges = [];
    this.edgeBy = {};
    for (var key in edgeSet) {
      var parts = key.split('-');
      this.edges.push({ a: +parts[0], b: +parts[1], ph: rand(0, TAU) });
      this.edgeBy[key] = this.edges.length - 1;
    }
    this.adj = [];
    for (var ci = 0; ci < cells.length; ci++) this.adj.push([]);
    for (var ei = 0; ei < this.edges.length; ei++) {
      var ed = this.edges[ei];
      this.adj[ed.a].push(ed.b);
      this.adj[ed.b].push(ed.a);
    }

    // service metadata: each puff is a deployment, each cell one of its pods
    var svcPool = SVC_POOL.slice().sort(function () { return Math.random() - 0.5; });
    var counts = [];
    for (var mc = 0; mc < cells.length; mc++) counts[cells[mc].group] = (counts[cells[mc].group] || 0) + 1;
    for (var mg = 0; mg < groups.length; mg++) {
      groups[mg].meta = {
        svc: svcPool[mg % svcPool.length],
        ns: NS_POOL[mg % NS_POOL.length],
        replicas: counts[mg] || 0
      };
    }
    for (var mp = 0; mp < cells.length; mp++) {
      cells[mp].meta = {
        pod: groups[cells[mp].group].meta.svc + '-' + podHash(),
        age: AGE_POOL[randInt(0, AGE_POOL.length - 1)],
        restarts: 0
      };
    }

    // ambient dust — telemetry noise filling the volume
    this.dust = [];
    var dustN = clamp(Math.round((w * h) / 9000), 70, 160);
    for (var di = 0; di < dustN; di++) {
      var ddx2 = rand(-1, 1), ddy2 = rand(-1, 1), ddz2 = rand(-1, 1);
      var ddl = Math.sqrt(ddx2 * ddx2 + ddy2 * ddy2 + ddz2 * ddz2) || 1;
      var drr = R * 1.05 * Math.cbrt(Math.random());
      this.dust.push({
        ax: (ddx2 / ddl) * drr * SX, ay: (ddy2 / ddl) * drr * SY, az: (ddz2 / ddl) * drr,
        amp: rand(3, 9), f1: rand(0.05, 0.16), f2: rand(0.04, 0.13),
        ph1: rand(0, TAU), ph2: rand(0, TAU), phz: rand(0, TAU), fz: rand(0.04, 0.1),
        tw: rand(0.4, 1.6), twp: rand(0, TAU),
        sz: rand(0.6, 1.7), col: Math.random() < 0.8 ? '170, 235, 225' : '190, 215, 245'
      });
    }

    this.pulses = [];
    this.particles = [];
    this.heartbeats = [];
    this.litPath = [];
    this.streams = [];
    this.waves = [];
    // aurora — vast barely-there color fields for depth
    this.aurora = [
      { x: 0.22, y: 0.30, r: this.w * 0.45, col: '36, 160, 160', f1: 0.05, f2: 0.04, p1: 1.2, p2: 4.1 },
      { x: 0.80, y: 0.60, r: this.h * 0.55, col: '40, 110, 190', f1: 0.04, f2: 0.06, p1: 2.8, p2: 0.7 },
      { x: 0.55, y: 0.85, r: this.w * 0.40, col: '30, 150, 140', f1: 0.06, f2: 0.05, p1: 5.0, p2: 2.2 }
    ];
    this.cellOrder = cells.map(function (_, i) { return i; });
    this.cellOrder.sort(function (p, q) { return cells[p].delay - cells[q].delay; });

    var start = randInt(0, cells.length - 1);
    this.agentScreen = null;
    this.agent = {
      cellIdx: start, x: cells[start].ax, y: cells[start].ay, z: cells[start].az,
      path: [], seg: 0, speed: 90, mode: 'patrol',
      holdUntil: 0, tail: [], visible: false
    };

    if (this.incidents.length) {
      this.incidents = [];
      if (this.hooks.onCancel) this.hooks.onCancel();
    }
    this.nextIncidentAt = this.ready ? this.time + rand(3, 5) : Infinity;
  };

  ClusterEngine.prototype.onResize = function () {
    this.resize();
    this.build();
    if (this.genesis) {
      this.genesis = null;
      for (var i = 0; i < this.cells.length; i++) { this.cells[i].born = 1; }
      this.setReady();
    }
    this.render();
  };

  /* ── genesis (entry animation) ──────────────────────────── */

  ClusterEngine.prototype.startGenesis = function (sx, sy) {
    this.awaitingGenesis = false;
    if (this.calm) { this.setReady(); return; }
    var cells = this.cells;
    // screen seed → world point on the z=0 plane (rest camera)
    var wx = sx - this.w * 0.5, wy = sy - this.h * 0.46, wz = 0;
    var maxD = 1;
    for (var i = 0; i < cells.length; i++) {
      cells[i].dist = dist3(wx, wy, wz, cells[i].ax, cells[i].ay, cells[i].az);
      maxD = Math.max(maxD, cells[i].dist);
    }
    var sorted = cells.map(function (_, i) { return i; })
      .sort(function (p, q) { return cells[p].dist - cells[q].dist; });
    for (var s = 0; s < sorted.length; s++) {
      var idx = sorted[s];
      cells[idx].delay = 0.08 + (cells[idx].dist / maxD) * 1.45 + rand(0, 0.22);
      cells[idx].x = null; cells[idx].born = 0;
      var parent = -1, pd = Infinity;
      for (var q2 = 0; q2 < s; q2++) {
        var d = dist3(cells[idx].ax, cells[idx].ay, cells[idx].az,
                      cells[sorted[q2]].ax, cells[sorted[q2]].ay, cells[sorted[q2]].az);
        if (d < pd) { pd = d; parent = sorted[q2]; }
      }
      cells[idx].parentIdx = parent;
    }
    this.cellOrder = sorted.slice();
    this.cellOrder.sort(function (p, q) { return cells[p].delay - cells[q].delay; });
    this.genesis = { sx: wx, sy: wy, sz: wz, start: this.time, end: this.time + 0.08 + 1.45 + 0.22 + 0.52 };
  };

  ClusterEngine.prototype.setReady = function () {
    if (this.ready) return;
    this.ready = true;
    this.agent.visible = !this.calm;
    this.nextIncidentAt = this.calm ? Infinity : this.time + 6;
    this.hbTimer = 1.2;
    if (this.hooks.onReady) this.hooks.onReady();
  };

  /* ── incidents ──────────────────────────────────────────── */

  ClusterEngine.prototype.pickIncidentCell = function () {
    var cells = this.cells;
    var used = {};
    var usedGroups = {};
    for (var u = 0; u < this.incidents.length; u++) {
      used[this.incidents[u].cellIdx] = 1;
      usedGroups[cells[this.incidents[u].cellIdx].group] = 1; // one per service puff
    }
    var cand = [];
    for (var i = 0; i < cells.length; i++) {
      var c = cells[i];
      if (used[i]) continue;
      if (usedGroups[c.group]) continue;      // concurrent anomalies hit different services
      if (c.sx == null || c.sd < 0.6) continue; // skip far, unreadable cells
      if (c.sx < 250 || c.sx > this.w - 250) continue;
      if (c.sy < 70 || c.sy > this.h - 190) continue;
      cand.push(i);
    }
    if (!cand.length) return -1;
    var best = [];
    for (var j = 0; j < cand.length; j++) {
      if (cells[cand[j]].degree >= 3) best.push(cand[j]);
    }
    var pool = best.length ? best : cand;
    return pool[randInt(0, pool.length - 1)];
  };

  ClusterEngine.prototype.spawnIncident = function () {
    var list = this.opts.incidents || [];
    if (!list.length) { this.nextIncidentAt = Infinity; return false; }
    var idx = this.pickIncidentCell();
    if (idx < 0) return false;
    var data = list[this.incidentSeq++ % list.length];
    var cell = this.cells[idx];
    cell.state = data.kind === 'mutation' ? 'warn' : 'sick';
    cell.healT = 0;
    if (cell.meta) cell.meta.restarts = randInt(8, 24);
    if (data.kind === 'pathogen') {
      cell.pathogen = { ang: rand(0, TAU), rot: rand(0, TAU), size: rand(7.5, 10.5), spikes: 9 };
    }
    // anomaly shockwaves
    var grp = this.groups[cell.group];
    this.waves.push(
      { x: cell.ax, y: cell.ay, z: cell.az, t0: this.time, ux: grp.ux, uy: grp.uy, uz: grp.uz, vx: grp.vx, vy: grp.vy, vz: grp.vz },
      { x: cell.ax, y: cell.ay, z: cell.az, t0: this.time + 0.28, ux: grp.ux, uy: grp.uy, uz: grp.uz, vx: grp.vx, vy: grp.vy, vz: grp.vz }
    );
    this.incidents.push({
      data: data, cellIdx: idx, phase: 'signal', phaseT: 0,
      emitted: [false, false, false], verdictDone: false, healed: false, reported: false
    });
    if (this.hooks.onPhase) this.hooks.onPhase('signal', data, this.cellPos(idx));
    // continuous spawner: next anomaly shows up on its own clock,
    // possibly while the agent is still investigating this one
    this.nextIncidentAt = this.time + rand(6, 12);
    if (this.agent.mode === 'patrol') this.dispatchAgent();
    return true;
  };

  ClusterEngine.prototype.dispatchAgent = function () {
    // rush to the first incident still waiting
    for (var i = 0; i < this.incidents.length; i++) {
      var inc = this.incidents[i];
      if (inc.phase !== 'signal') continue;
      inc.phase = 'dispatch';
      inc.phaseT = 0;
      var ag = this.agent;
      if (ag && ag.visible) {
        ag.path = this.bfs(ag.cellIdx, inc.cellIdx);
        ag.seg = 0;
        ag.speed = 450;
        ag.mode = 'respond';
        this.activeTarget = inc;
      } else {
        inc.phase = 'evidence';
        inc.phaseT = 0;
      }
      if (this.hooks.onPhase) this.hooks.onPhase('respond', inc.data, this.cellPos(inc.cellIdx));
      return true;
    }
    return false;
  };

  ClusterEngine.prototype.cellPos = function (i) {
    var c = this.cells[i];
    return c ? { x: c.sx || 0, y: c.sy || 0 } : { x: 0, y: 0 };
  };

  ClusterEngine.prototype.bfs = function (a, b) {
    if (a === b) return [a];
    var prev = new Array(this.cells.length).fill(-1);
    var q = [a];
    prev[a] = a;
    var found = false;
    while (q.length && !found) {
      var u = q.shift();
      var nb = this.adj[u];
      for (var i = 0; i < nb.length; i++) {
        var v = nb[i];
        if (prev[v] !== -1) continue;
        prev[v] = u;
        if (v === b) { found = true; break; }
        q.push(v);
      }
    }
    if (prev[b] === -1) return [b];
    var path = [];
    var cur = b;
    while (cur !== a) { path.push(cur); cur = prev[cur]; }
    path.push(a);
    path.reverse();
    return path;
  };

  ClusterEngine.prototype.lightEdge = function (i, j) {
    var key = i < j ? i + '-' + j : j + '-' + i;
    var ei = this.edgeBy[key];
    if (ei == null) return;
    this.litPath.push({ idx: ei, until: this.time + 2.6 });
  };

  /* ── sim update ─────────────────────────────────────────── */

  ClusterEngine.prototype.update = function (dt) {
    this.time += dt;

    // stage 1 — dark stage
    if (this.awaitingGenesis) return;

    if (this.auroraIn < 1) this.auroraIn = Math.min(1, this.auroraIn + dt * 0.45);

    this.updateCamera(dt);

    var cells = this.cells;
    var i, c;

    // cells: 3D drift → project
    for (var oi = 0; oi < this.cellOrder.length; oi++) {
      c = cells[this.cellOrder[oi]];
      var dx = 0, dy = 0, dz = 0;
      if (!this.calm) {
        dx = Math.sin(this.time * c.f1 + c.ph1) * c.amp + Math.sin(this.time * c.f2 + c.ph2) * c.amp2;
        dy = Math.cos(this.time * c.f3 + c.ph3) * c.amp + Math.cos(this.time * c.f2 * 0.8 + c.ph2) * c.amp2;
        dz = Math.sin(this.time * c.fz + c.phz) * c.amp * 0.7;
      }
      var tx = c.ax + dx, ty = c.ay + dy, tz = c.az + dz;
      var born = 1;
      if (this.genesis) {
        var age = (this.time - this.genesis.start - c.delay) / 0.52;
        if (age <= 0) { c.x = null; c.sx = null; continue; }
        if (age < 1) {
          born = clamp(age, 0, 1);
          var p = c.parentIdx >= 0 ? cells[c.parentIdx] : null;
          var px = p && p.x != null ? p.x : this.genesis.sx;
          var py = p && p.y != null ? p.y : this.genesis.sy;
          var pz = p && p.z != null ? p.z : this.genesis.sz;
          var kk = easeOutCubic(born);
          tx = px + (tx - px) * kk; ty = py + (ty - py) * kk; tz = pz + (tz - pz) * kk;
          born = easeOutBack(born);
        }
      }
      c.born = born;
      c.x = tx; c.y = ty; c.z = tz;
      var pr = this.project(tx, ty, tz);
      c.sx = pr.ok ? pr.x : null;   // behind-camera cells vanish (edges skip them too)
      c.sy = pr.y; c.sd = pr.d; c.sz = pr.z;

      if (c.state === 'warn' || c.state === 'sick') {
        c.flicker = 0.55 + 0.45 * Math.sin(this.time * 8.5 + c.s1);
      }
      if (c.state === 'healing') {
        c.healT += dt;
        if (c.healT > 1.5) { c.state = 'calm'; c.glow = 1; }
      }
      if (c.glow > 0) c.glow = Math.max(0, c.glow - dt * 1.1);
      if (c.pathogen) c.pathogen.rot += dt * 0.9;
    }

    // hover pick — tooltips point at nodes without touching the camera
    if (this.hoverEnabled && this.px != null && this.ready) {
      var hBest = -1, hBestD = Infinity;
      for (i = 0; i < cells.length; i++) {
        var hc = cells[i];
        if (hc.sx == null) continue;
        var hdx = hc.sx - this.px, hdy = hc.sy - this.py;
        var hd2 = hdx * hdx + hdy * hdy;
        var hr = Math.max(10, hc.r * hc.sd + 7);
        if (i === this.hoverIdx) hr *= 1.4; // sticky: small drift won't drop it
        if (hd2 < hr * hr && hd2 < hBestD) { hBestD = hd2; hBest = i; }
      }
      this.hoverIdx = hBest;
      if (hBest >= 0) cells[hBest].glow = Math.max(cells[hBest].glow, 0.5);
    } else {
      this.hoverIdx = -1; // pointer gone / drag / pre-ready → no tooltip
    }

    if (this.genesis && this.time > this.genesis.end) {
      this.genesis = null;
      for (i = 0; i < cells.length; i++) cells[i].born = 1;
      this.setReady();
    }

    // heartbeats
    this.hbTimer -= dt;
    if (this.ready && this.hbTimer <= 0) {
      this.hbTimer = rand(7, 11);
      var hc = cells[randInt(0, cells.length - 1)];
      this.heartbeats.push({ x: hc.ax, y: hc.ay, z: hc.az, t: this.time });
    }
    for (i = this.heartbeats.length - 1; i >= 0; i--) {
      if ((this.time - this.heartbeats[i].t) * 230 > Math.max(this.w, this.h) * 1.4) {
        this.heartbeats.splice(i, 1);
      }
    }

    // ambient pulses (traffic)
    this.pulseTimer -= dt;
    if (this.pulseTimer <= 0 && this.ready && !this.calm && this.edges.length) {
      this.pulseTimer = rand(0.18, 0.42);
      var pe = this.edges[randInt(0, this.edges.length - 1)];
      this.pulses.push({ e: pe, p: 0, sp: rand(0.5, 1.0), fwd: Math.random() < 0.5, red: false });
    }
    // red pulses converging on every unresolved anomaly
    for (var sgi = 0; sgi < this.incidents.length; sgi++) {
      var sinc = this.incidents[sgi];
      if (sinc.phase !== 'signal' && sinc.phase !== 'dispatch') continue;
      var ic = sinc.cellIdx;
      var nb = this.adj[ic] || [];
      if (nb.length && Math.random() < 0.18) {
        var from = nb[randInt(0, nb.length - 1)];
        var key = from < ic ? from + '-' + ic : ic + '-' + from;
        var pei = this.edgeBy[key];
        if (pei != null) {
          var pe2 = this.edges[pei];
          this.pulses.push({ e: pe2, p: 0, sp: rand(0.9, 1.4), fwd: pe2.a === ic, red: true });
        }
      }
    }
    for (i = this.pulses.length - 1; i >= 0; i--) {
      var pu = this.pulses[i];
      pu.p += pu.sp * dt;
      if (pu.p >= 1) this.pulses.splice(i, 1);
    }

    // synapse streams
    this.streamTimer -= dt;
    if (this.ready && !this.calm && this.streamTimer <= 0 && this.streams.length < 3) {
      this.streamTimer = rand(3.5, 7);
      var sc = cells[randInt(0, cells.length - 1)];
      var sa = rand(0, TAU), sb2 = rand(-0.7, 0.7);
      this.streams.push({
        x: sc.ax, y: sc.ay, z: sc.az,
        vx: Math.cos(sa) * rand(260, 460),
        vy: Math.sin(sa) * rand(160, 300),
        vz: sb2 * rand(120, 260),
        life: rand(1.1, 1.8), max: 1.8
      });
    }
    for (i = this.streams.length - 1; i >= 0; i--) {
      var st = this.streams[i];
      st.x += st.vx * dt; st.y += st.vy * dt; st.z += st.vz * dt;
      st.life -= dt;
      if (st.life <= 0) this.streams.splice(i, 1);
    }

    // shockwaves
    for (i = this.waves.length - 1; i >= 0; i--) {
      if (this.time - this.waves[i].t0 > 1.2) this.waves.splice(i, 1);
    }

    // heal particles (world space)
    for (i = this.particles.length - 1; i >= 0; i--) {
      var pt = this.particles[i];
      pt.x += pt.vx * dt; pt.y += pt.vy * dt; pt.z += pt.vz * dt;
      pt.vy += 26 * dt;
      pt.life -= dt;
      if (pt.life <= 0) this.particles.splice(i, 1);
    }

    // lit path fade
    for (i = this.litPath.length - 1; i >= 0; i--) {
      if (this.time > this.litPath[i].until) this.litPath.splice(i, 1);
    }

    this.updateAgent(dt);

    // continuous spawner — new anomalies appear mid-investigation, capped at 3,
    // always in a service puff that doesn't already host a live anomaly
    if (this.ready && !this.calm && this.time > this.nextIncidentAt) {
      if (this.incidents.length < 3) {
        if (!this.spawnIncident()) this.nextIncidentAt = this.time + rand(2, 3);
      } else {
        this.nextIncidentAt = this.time + rand(2, 4);
      }
    }
    if (this.incidents.length) this.updateIncidents(dt);
  };

  ClusterEngine.prototype.updateAgent = function (dt) {
    var ag = this.agent;
    if (!ag || !ag.visible) return;
    var cells = this.cells;

    if (ag.mode === 'hold') {
      var c = cells[this.activeTarget ? this.activeTarget.cellIdx : ag.cellIdx];
      if (c && c.x != null) {
        var oa = this.time * 1.4;
        ag.x = c.x + Math.cos(oa) * (c.r + 26);
        ag.y = c.y + Math.sin(oa) * (c.r + 26) * 0.7;
        ag.z = c.z + Math.sin(oa * 0.6) * 12;
      }
    } else {
      if (!ag.path.length) {
        if (this.time > ag.holdUntil) {
          var target = randInt(0, cells.length - 1);
          if (cells[target].x != null) {
            ag.path = this.bfs(ag.cellIdx, target);
            ag.seg = 0;
            ag.speed = 128;
          }
        }
      } else {
        var wp = cells[ag.path[ag.seg]];
        if (wp.x == null) { ag.path = []; return; }
        var dx = wp.x - ag.x, dy = wp.y - ag.y, dz = wp.z - ag.z;
        var d = Math.sqrt(dx * dx + dy * dy + dz * dz);
        var step = ag.speed * dt;
        if (d <= step) {
          ag.x = wp.x; ag.y = wp.y; ag.z = wp.z;
          var fromIdx = ag.path[ag.seg];
          ag.cellIdx = ag.path[ag.seg];
          ag.seg++;
          if (ag.mode === 'respond') this.lightEdge(fromIdx, ag.cellIdx);
          if (ag.seg >= ag.path.length) {
            ag.path = [];
            if (ag.mode === 'respond') {
              ag.mode = 'hold';
              if (this.activeTarget) { this.activeTarget.phase = 'evidence'; this.activeTarget.phaseT = 0; }
            } else {
              ag.holdUntil = this.time + rand(0.8, 2.2);
            }
          }
        } else {
          ag.x += (dx / d) * step;
          ag.y += (dy / d) * step;
          ag.z += (dz / d) * step;
        }
      }
    }

    ag.tail.unshift({ x: ag.x, y: ag.y, z: ag.z });
    if (ag.tail.length > 26) ag.tail.pop();

    // the camera operator needs to know where the subject is
    var hp = this.project(ag.x, ag.y, ag.z);
    this.agentScreen = { x: hp.x, y: hp.y, ok: hp.ok, z: hp.z };
  };

  ClusterEngine.prototype.updateIncidents = function (dt) {
    var ag = this.agent;
    for (var i = this.incidents.length - 1; i >= 0; i--) {
      var inc = this.incidents[i];
      if (inc.phase === 'signal' || inc.phase === 'dispatch') continue; // waiting / en route
      if (inc !== this.activeTarget) continue; // frozen until the agent holds it
      inc.phaseT += dt;
      var cell = this.cells[inc.cellIdx];
      var pos = this.cellPos(inc.cellIdx);

      for (var k = 0; k < inc.data.evidence.length; k++) {
        if (!inc.emitted[k] && inc.phaseT > k * 0.7) {
          inc.emitted[k] = true;
          if (this.hooks.onEvidence) this.hooks.onEvidence(k, inc.data, pos);
        }
      }
      if (!inc.verdictDone && inc.phaseT > inc.data.evidence.length * 0.7 + 0.9) {
        inc.verdictDone = true;
        if (this.hooks.onVerdict) this.hooks.onVerdict(inc.data, pos);
      }
      if (!inc.healed && inc.phaseT > inc.data.evidence.length * 0.7 + 1.7) {
        inc.healed = true;
        cell.state = 'healing'; cell.healT = 0; cell.glow = 1.2;
        cell.memory = true; // immune memory: healed pods keep a whisper of gold
        if (cell.pathogen) {
          for (var s = 0; s < 14; s++) {
            this.particles.push({
              x: cell.x, y: cell.y, z: cell.z,
              vx: rand(-46, 46), vy: rand(-60, 12), vz: rand(-30, 30),
              life: rand(0.5, 1.1), r: rand(1, 2.4)
            });
          }
          cell.pathogen = null;
        }
      }
      if (!inc.reported && inc.phaseT > inc.data.evidence.length * 0.7 + 2.1) {
        inc.reported = true;
        if (this.hooks.onReport) this.hooks.onReport(inc.data, pos);
      }
      if (inc.phaseT > inc.data.evidence.length * 0.7 + 5.6) {
        var data = inc.data;
        this.incidents.splice(i, 1);
        if (this.activeTarget === inc) this.activeTarget = null;
        if (this.hooks.onDock) this.hooks.onDock(data);
        // straight to the next anomaly, if any
        if (!this.dispatchAgent()) {
          ag.mode = 'patrol';
          ag.holdUntil = this.time + 2;
        }
      }
    }
  };

  /* ── render ─────────────────────────────────────────────── */

  ClusterEngine.prototype.fog = function (z) {
    return clamp(1 - (z / ((this.R || 700) * 1.6)) * 0.75, 0.35, 1);
  };

  ClusterEngine.prototype.render = function () {
    var ctx = this.ctx;
    var cells = this.cells;
    ctx.clearRect(0, 0, this.w, this.h);

    // aurora — slow color fields under everything
    if (this.aurora && this.auroraIn > 0.01) {
      var tf = this.calm ? 0 : this.time;
      ctx.globalCompositeOperation = 'lighter';
      for (var ai = 0; ai < this.aurora.length; ai++) {
        var A = this.aurora[ai];
        var axp = A.x * this.w + Math.sin(tf * A.f1 + A.p1) * this.w * 0.06;
        var ayp = A.y * this.h + Math.cos(tf * A.f2 + A.p2) * this.h * 0.05;
        var ag = ctx.createRadialGradient(axp, ayp, 0, axp, ayp, A.r);
        ag.addColorStop(0, 'rgba(' + A.col + ', ' + (0.05 * this.auroraIn) + ')');
        ag.addColorStop(1, 'rgba(' + A.col + ', 0)');
        ctx.fillStyle = ag;
        ctx.beginPath(); ctx.arc(axp, ayp, A.r, 0, TAU); ctx.fill();
      }
      ctx.globalCompositeOperation = 'source-over';
    }

    var i, e, ca, cb;

    // edges — depth-faded hairlines
    for (i = 0; i < this.edges.length; i++) {
      e = this.edges[i];
      ca = cells[e.a]; cb = cells[e.b];
      if (ca.sx == null || cb.sx == null) continue;
      var bornF = Math.min(ca.born, cb.born);
      if (bornF <= 0.05) continue;
      var fogE = this.fog((ca.sz + cb.sz) / 2);
      var alpha = (0.14 + 0.06 * Math.sin(this.time * 0.6 + e.ph)) * bornF * fogE;
      var sick = (ca.state === 'sick' || ca.state === 'warn' || cb.state === 'sick' || cb.state === 'warn');
      ctx.strokeStyle = sick
        ? 'rgba(255, 93, 108, ' + (0.35 * bornF * fogE) + ')'
        : 'rgba(130, 220, 210, ' + Math.min(alpha, 0.6) + ')';
      ctx.lineWidth = Math.max(0.5, 1 * ((ca.sd + cb.sd) / 2));
      ctx.beginPath();
      ctx.moveTo(ca.sx, ca.sy);
      ctx.lineTo(cb.sx, cb.sy);
      ctx.stroke();
    }

    // lit path (gold — the traceable chain)
    ctx.globalCompositeOperation = 'lighter';
    for (i = 0; i < this.litPath.length; i++) {
      var le = this.edges[this.litPath[i].idx];
      if (!le) continue;
      var la = cells[le.a], lb = cells[le.b];
      if (la.sx == null || lb.sx == null) continue;
      var fade = clamp((this.litPath[i].until - this.time) / 2.6, 0, 1);
      ctx.strokeStyle = 'rgba(255, 217, 138, ' + (0.5 * fade) + ')';
      ctx.lineWidth = 1.6;
      ctx.beginPath();
      ctx.moveTo(la.sx, la.sy);
      ctx.lineTo(lb.sx, lb.sy);
      ctx.stroke();
    }

    // shockwaves — anomaly rings expanding in the tissue plane
    for (i = 0; i < this.waves.length; i++) {
      var wv = this.waves[i];
      var age = this.time - wv.t0;
      if (age < 0) continue;
      var wcenter = this.project(wv.x, wv.y, wv.z);
      if (!wcenter.ok) continue;
      var R = age * 230;
      var alp = (1 - age / 1.2) * 0.32;
      // sample 28 points of the 3D circle, project, stroke
      ctx.strokeStyle = 'rgba(255, 93, 108, ' + alp + ')';
      ctx.lineWidth = 1.2;
      ctx.beginPath();
      var first = true;
      for (var wi = 0; wi <= 28; wi++) {
        var wa = (wi / 28) * TAU;
        var wox = Math.cos(wa) * R, woy = Math.sin(wa) * R;
        var px2 = wv.x + wv.ux * wox + wv.vx * woy;
        var py2 = wv.y + wv.uy * wox + wv.vy * woy;
        var pz2 = wv.z + wv.uz * wox + wv.vz * woy;
        var pp = this.project(px2, py2, pz2);
        if (first) { ctx.moveTo(pp.x, pp.y); first = false; }
        else ctx.lineTo(pp.x, pp.y);
      }
      ctx.stroke();
    }

    // ambient dust — telemetry noise
    var dtf = this.calm ? 0 : this.time;
    for (i = 0; i < this.dust.length; i++) {
      var du = this.dust[i];
      var ddx = Math.sin(dtf * du.f1 + du.ph1) * du.amp;
      var ddy = Math.cos(dtf * du.f2 + du.ph2) * du.amp;
      var ddz = Math.sin(dtf * du.fz + du.phz) * du.amp * 0.6;
      var dp = this.project(du.ax + ddx, du.ay + ddy, du.az + ddz);
      if (!dp.ok) continue;
      if (dp.x < -20 || dp.x > this.w + 20 || dp.y < -20 || dp.y > this.h + 20) continue;
      var tw = 0.5 + 0.5 * Math.sin(dtf * du.tw + du.twp);
      var da = (0.05 + tw * 0.13) * this.fog(dp.z) * this.auroraIn;
      ctx.fillStyle = 'rgba(' + du.col + ', ' + da + ')';
      ctx.beginPath(); ctx.arc(dp.x, dp.y, du.sz * dp.d, 0, TAU); ctx.fill();
    }

    // synapse streams
    for (i = 0; i < this.streams.length; i++) {
      var st = this.streams[i];
      var sp = this.project(st.x, st.y, st.z);
      var sq = this.project(st.x - st.vx * 0.12, st.y - st.vy * 0.12, st.z - st.vz * 0.12);
      var sal = clamp(st.life / st.max, 0, 1) * 0.3 * this.fog(sp.z);
      var sg = ctx.createLinearGradient(sq.x, sq.y, sp.x, sp.y);
      sg.addColorStop(0, 'rgba(140, 240, 225, 0)');
      sg.addColorStop(1, 'rgba(140, 240, 225, ' + sal + ')');
      ctx.strokeStyle = sg;
      ctx.lineWidth = 1;
      ctx.beginPath(); ctx.moveTo(sq.x, sq.y); ctx.lineTo(sp.x, sp.y); ctx.stroke();
    }

    // pulses (traffic)
    for (i = 0; i < this.pulses.length; i++) {
      var pu = this.pulses[i];
      var pa = cells[pu.e.a], pb = cells[pu.e.b];
      if (pa.x == null || pb.x == null) continue;
      var t = pu.fwd ? pu.p : 1 - pu.p;
      var wxp = pa.x + (pb.x - pa.x) * t;
      var wyp = pa.y + (pb.y - pa.y) * t;
      var wzp = pa.z + (pb.z - pa.z) * t;
      var pp2 = this.project(wxp, wyp, wzp);
      if (!pp2.ok) continue;
      var fadeP = Math.sin(pu.p * Math.PI);
      var pcol = pu.red ? '255, 93, 108' : '120, 240, 225';
      var g2 = ctx.createRadialGradient(pp2.x, pp2.y, 0, pp2.x, pp2.y, 7 * pp2.d);
      g2.addColorStop(0, 'rgba(' + pcol + ', ' + (0.75 * fadeP * this.fog(pp2.z)) + ')');
      g2.addColorStop(1, 'rgba(' + pcol + ', 0)');
      ctx.fillStyle = g2;
      ctx.beginPath(); ctx.arc(pp2.x, pp2.y, 7 * pp2.d, 0, TAU); ctx.fill();
    }

    // heal particles
    for (i = 0; i < this.particles.length; i++) {
      var pt = this.particles[i];
      var ptp = this.project(pt.x, pt.y, pt.z);
      var g3 = ctx.createRadialGradient(ptp.x, ptp.y, 0, ptp.x, ptp.y, 6);
      g3.addColorStop(0, 'rgba(255, 217, 138, ' + (0.8 * pt.life) + ')');
      g3.addColorStop(1, 'rgba(255, 217, 138, 0)');
      ctx.fillStyle = g3;
      ctx.beginPath(); ctx.arc(ptp.x, ptp.y, 6, 0, TAU); ctx.fill();
    }
    ctx.globalCompositeOperation = 'source-over';

    // cells — painter's order, far to near
    var order = [];
    for (i = 0; i < cells.length; i++) {
      if (cells[i].sx != null) order.push(i);
    }
    order.sort(function (p, q) { return cells[q].sz - cells[p].sz; });
    for (i = 0; i < order.length; i++) {
      this.drawCell(ctx, cells[order[i]]);
    }

    // agent on top
    if (this.agent && this.agent.visible) this.drawAgent(ctx);
  };

  ClusterEngine.prototype.heartbeatBoost = function (c) {
    var boost = 0;
    for (var i = 0; i < this.heartbeats.length; i++) {
      var hb = this.heartbeats[i];
      var age = (this.time - hb.t) * 230;
      var d = Math.abs(dist3(c.x, c.y, c.z, hb.x, hb.y, hb.z) - age);
      var band = Math.exp(-(d * d) / (2 * 62 * 62));
      var fade = clamp(1 - age / (Math.max(this.w, this.h) * 1.4), 0, 1);
      boost += band * 0.55 * fade;
    }
    return boost;
  };

  ClusterEngine.prototype.drawCell = function (ctx, c) {
    var col;
    if (c.state === 'sick') col = '255, 93, 108';
    else if (c.state === 'warn') col = c.flicker > 0.5 ? '255, 180, 84' : '255, 110, 90';
    else if (c.state === 'healing') col = '255, 217, 138';
    else col = c.memory ? mixCol(c.col, '255, 217, 138', 0.22) : c.col;

    var boost = c.glow + this.heartbeatBoost(c);
    var r = c.r * c.born * c.sd;
    if (r < 0.5) return;

    var fog = this.fog(c.sz);
    var flick = (c.state === 'warn' || c.state === 'sick') ? (0.7 + 0.3 * c.flicker) : 1;
    var dim = c.dim * flick * fog;

    // ── light orb: hot core melting into a soft bloom ──
    ctx.globalCompositeOperation = 'lighter';

    var bloom = ctx.createRadialGradient(c.sx, c.sy, r * 0.2, c.sx, c.sy, r * 2.3);
    bloom.addColorStop(0, 'rgba(' + col + ', ' + ((0.10 + boost * 0.16) * dim) + ')');
    bloom.addColorStop(1, 'rgba(' + col + ', 0)');
    ctx.fillStyle = bloom;
    ctx.beginPath(); ctx.arc(c.sx, c.sy, r * 2.3, 0, TAU); ctx.fill();

    var core = ctx.createRadialGradient(c.sx, c.sy, 0, c.sx, c.sy, r);
    core.addColorStop(0, 'rgba(' + mixWhite(col, 0.55) + ', ' + (0.88 * dim) + ')');
    core.addColorStop(0.4, 'rgba(' + col + ', ' + (0.42 * dim) + ')');
    core.addColorStop(1, 'rgba(' + col + ', 0)');
    ctx.fillStyle = core;
    ctx.beginPath(); ctx.arc(c.sx, c.sy, r, 0, TAU); ctx.fill();

    ctx.globalCompositeOperation = 'source-over';

    // satellites — replicas orbiting their node
    if (c.sat > 0 && c.state === 'calm' && c.born >= 1) {
      var grp = this.groups[c.group];
      var nSat = c.sat;
      for (var s2 = 0; s2 < nSat; s2++) {
        var sa = this.time * (0.5 + s2 * 0.22) + c.s1 + s2 * Math.PI;
        var sor = c.r + 11 + s2 * 3;
        var sox = Math.cos(sa) * sor, soy = Math.sin(sa) * sor;
        var sp2 = this.project(
          c.x + grp.ux * sox + grp.vx * soy,
          c.y + grp.uy * sox + grp.vy * soy,
          c.z + grp.uz * sox + grp.vz * soy);
        if (!sp2.ok) continue;
        ctx.fillStyle = 'rgba(' + col + ', ' + (0.30 * fog) + ')';
        ctx.beginPath(); ctx.arc(sp2.x, sp2.y, Math.max(0.7, 1.3 * sp2.d), 0, TAU); ctx.fill();
      }
    }

    // infection ring — the one deliberate line when a cell is unwell
    if (c.state === 'sick' || c.state === 'warn') {
      ctx.strokeStyle = 'rgba(255, 93, 108, ' + (0.30 + 0.2 * c.flicker) + ')';
      ctx.lineWidth = 1.2;
      ctx.beginPath();
      ctx.arc(c.sx, c.sy, r + 5 + Math.sin(this.time * 5 + c.s2) * 1.5, 0, TAU);
      ctx.stroke();
    }

    // pathogen
    if (c.pathogen) {
      var pxp = c.sx + Math.cos(c.pathogen.ang) * (r + c.pathogen.size + 3);
      var pyp = c.sy + Math.sin(c.pathogen.ang) * (r + c.pathogen.size + 3);
      var spikes = c.pathogen.spikes;
      var outer = c.pathogen.size * (1 + 0.12 * Math.sin(this.time * 6)) * c.sd;
      var inner = outer * 0.45;
      ctx.globalCompositeOperation = 'lighter';
      var pg = ctx.createRadialGradient(pxp, pyp, 0, pxp, pyp, outer * 3);
      pg.addColorStop(0, 'rgba(255, 77, 109, 0.5)');
      pg.addColorStop(1, 'rgba(255, 77, 109, 0)');
      ctx.fillStyle = pg;
      ctx.beginPath(); ctx.arc(pxp, pyp, outer * 3, 0, TAU); ctx.fill();
      ctx.globalCompositeOperation = 'source-over';
      ctx.beginPath();
      for (var sp = 0; sp < spikes * 2; sp++) {
        var a2 = c.pathogen.rot + (sp / (spikes * 2)) * TAU;
        var rr2 = sp % 2 === 0 ? outer : inner;
        var sx2 = pxp + Math.cos(a2) * rr2;
        var sy2 = pyp + Math.sin(a2) * rr2;
        if (sp === 0) ctx.moveTo(sx2, sy2); else ctx.lineTo(sx2, sy2);
      }
      ctx.closePath();
      ctx.fillStyle = 'rgba(214, 46, 74, 0.9)';
      ctx.fill();
      ctx.strokeStyle = 'rgba(255, 120, 140, 0.8)';
      ctx.lineWidth = 1;
      ctx.stroke();
    }
  };

  ClusterEngine.prototype.drawAgent = function (ctx) {
    var ag = this.agent;
    var i;
    var head = this.project(ag.x, ag.y, ag.z);
    var headOk = head.ok;

    // the protagonist is never allowed to drown in foreground glow:
    // a soft dark underlay keeps the comet legible over bright clusters
    ctx.globalCompositeOperation = 'source-over';
    for (i = ag.tail.length - 1; i > 0; i--) {
      var tp = this.project(ag.tail[i].x, ag.tail[i].y, ag.tail[i].z);
      var tq = this.project(ag.tail[i - 1].x, ag.tail[i - 1].y, ag.tail[i - 1].z);
      var f = 1 - i / ag.tail.length;
      ctx.strokeStyle = 'rgba(4, 9, 11, ' + (0.4 * f) + ')';
      ctx.lineWidth = 4.5 * f + 0.5;
      ctx.beginPath();
      ctx.moveTo(tp.x, tp.y);
      ctx.lineTo(tq.x, tq.y);
      ctx.stroke();
    }
    if (headOk) {
      var back = ctx.createRadialGradient(head.x, head.y, 0, head.x, head.y, 22);
      back.addColorStop(0, 'rgba(3, 7, 9, 0.6)');
      back.addColorStop(1, 'rgba(3, 7, 9, 0)');
      ctx.fillStyle = back;
      ctx.beginPath(); ctx.arc(head.x, head.y, 22, 0, TAU); ctx.fill();
    }

    // tail — world trail, projected (gold, additive)
    ctx.globalCompositeOperation = 'lighter';
    for (i = ag.tail.length - 1; i > 0; i--) {
      var tp2 = this.project(ag.tail[i].x, ag.tail[i].y, ag.tail[i].z);
      var tq2 = this.project(ag.tail[i - 1].x, ag.tail[i - 1].y, ag.tail[i - 1].z);
      var f2 = 1 - i / ag.tail.length;
      ctx.strokeStyle = 'rgba(255, 230, 176, ' + (0.36 * f2 * f2) + ')';
      ctx.lineWidth = 2.6 * f2;
      ctx.beginPath();
      ctx.moveTo(tp2.x, tp2.y);
      ctx.lineTo(tq2.x, tq2.y);
      ctx.stroke();
    }
    // head glow
    if (headOk) {
      var g = ctx.createRadialGradient(head.x, head.y, 0, head.x, head.y, 28);
      g.addColorStop(0, 'rgba(255, 240, 205, 0.9)');
      g.addColorStop(0.35, 'rgba(255, 217, 138, 0.38)');
      g.addColorStop(1, 'rgba(255, 217, 138, 0)');
      ctx.fillStyle = g;
      ctx.beginPath(); ctx.arc(head.x, head.y, 28, 0, TAU); ctx.fill();
      ctx.globalCompositeOperation = 'source-over';
      ctx.fillStyle = '#fffbe9';
      ctx.beginPath(); ctx.arc(head.x, head.y, 3.6, 0, TAU); ctx.fill();
      ctx.strokeStyle = 'rgba(255, 217, 138, 0.95)';
      ctx.lineWidth = 1.2;
      ctx.beginPath(); ctx.arc(head.x, head.y, 7, 0, TAU); ctx.stroke();
    } else {
      ctx.globalCompositeOperation = 'source-over';
    }
  };

  /* ── loop ───────────────────────────────────────────────── */

  ClusterEngine.prototype.frame = function (now) {
    if (!this.running) return;
    var dt = Math.min((now - this.last) / 1000, 0.05);
    this.last = now;
    try {
      this.update(dt);
      this.render();
    } catch (err) {
      // never let one bad frame kill the loop
      console.error('frame error:', err);
    }
    this.raf = requestAnimationFrame(this.frame);
  };

  ClusterEngine.prototype.start = function () {
    if (this.running) return;
    this.running = true;
    this.last = performance.now();
    this.raf = requestAnimationFrame(this.frame);
  };

  ClusterEngine.prototype.stop = function () {
    this.running = false;
    cancelAnimationFrame(this.raf);
  };

  window.ClusterEngine = ClusterEngine;
})();
