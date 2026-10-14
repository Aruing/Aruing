/* ═══════════════════════════════════════════════════════════════
   Aruing site — page orchestration.
   Wires the canvas engine to DOM chrome: entry boot sequence,
   incident ticker, evidence chips, verdict stamp, report cards,
   terminal theater typing, reveals, copy button.
   ═══════════════════════════════════════════════════════════════ */
(function () {
  'use strict';

  var REDUCED = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* ── incident archetypes (real scenario vocabulary) ─────── */
  var INCIDENTS = [
    {
      id: 'configmap-missing',
      kind: 'pathogen',
      ticker: 'anomaly · demo/demo-api-…9pq4z · CreateContainerConfigError',
      evidence: [
        ['kubectl describe pod demo-api-7d9f6b8c5-9pq4z -n demo', 'e_0191 · 61 lines'],
        ['kubectl get configmap demo-api-config -n demo', 'e_0192 · 12 lines'],
        ['kubectl get events -n demo --tail 8', 'e_0193 · 34 lines']
      ],
      report: {
        title: 'Report · run_01J8ZK7Q · demo-api',
        verdict: 'supported ✓ · e_0191 e_0192 e_0193',
        root: 'ConfigMap "demo-api-config" is missing key "config.yaml" — the container restart-loops waiting for its mount.',
        fix: 'Restore the key, then kubectl rollout restart deploy/demo-api.'
      }
    },
    {
      id: 'crashloop-bad-image',
      kind: 'mutation',
      ticker: 'anomaly · demo/demo-api-…9x4lm · ImagePullBackOff',
      evidence: [
        ['kubectl describe pod demo-api-9x4lm -n demo', 'e_0207 · 58 lines'],
        ['kubectl get events -n demo --tail 8', 'e_0208 · 41 lines']
      ],
      report: {
        title: 'Report · run_01J8ZP3F · demo-api',
        verdict: 'supported ✓ · e_0207 e_0208',
        root: 'Image tag demo-api:1.21.0 was never pushed — the registry answers 404 on every pull.',
        fix: 'Pin the image to the last known-good tag and restart the rollout.'
      }
    },
    {
      id: 'oom-killed',
      kind: 'pathogen',
      ticker: 'anomaly · demo/worker-batch-…c2v9n · OOMKilled (x12)',
      evidence: [
        ['kubectl describe pod worker-batch-c2v9n -n demo', 'e_0214 · 55 lines'],
        ['kubectl logs worker-batch-c2v9n -n demo --tail 20', 'e_0215 · 20 lines'],
        ['kubectl top pods -n demo', 'e_0216 · 9 lines']
      ],
      report: {
        title: 'Report · run_01J8ZR8M · worker-batch',
        verdict: 'supported ✓ · e_0214 e_0215 e_0216',
        root: 'The worker exceeds its 256Mi limit during compaction — OOMKilled each cycle, exit 137.',
        fix: 'Raise the limit to 512Mi or stream the compaction in batches.'
      }
    },
    {
      id: 'svc-wrong-selector',
      kind: 'mutation',
      ticker: 'anomaly · demo/demo-api · 503 · latency ↑',
      evidence: [
        ['kubectl get endpoints demo-api -n demo', 'e_0223 · 3 lines'],
        ['kubectl describe svc demo-api -n demo', 'e_0224 · 27 lines']
      ],
      report: {
        title: 'Report · run_01J8ZT2K · demo-api',
        verdict: 'supported ✓ · e_0223 e_0224',
        root: 'Service selector app=demo-api-web matches zero pods — endpoints empty, every request 503s.',
        fix: 'Fix the selector to app=demo-api and watch endpoints repopulate.'
      }
    }
  ];

  /* ── boot sequence ──────────────────────────────────────── */
  var BOOT = [
    { k: 'cmd', t: 'kind create cluster --name aruing-demo' },
    { k: 'out ok', t: 'control plane ready · 3.2s' },
    { k: 'cmd', t: 'kubectl apply -f demo-api.yaml' },
    { k: 'out', t: 'deployment.apps/demo-api created' },
    { k: 'cmd', t: 'kubectl get pods -n demo' },
    { k: 'out', t: 'demo-api-7d9f6b8c5-xk2lm   1/1   Running' },
    { k: 'out', t: 'demo-api-7d9f6b8c5-9pq4z   1/1   Running' }
  ];

  /* ── terminal theater script ────────────────────────────── */
  var CHAT = [
    { k: 'cmd', t: 'aruing chat' },
    { k: 'empty', t: '' },
    { k: 'you', t: 'hello — why is demo-api in demo unreachable?' },
    { k: 'empty', t: '' },
    { k: 'tool', t: 'kubectl get pods -n demo' },
    { k: 'out', t: 'demo-api-7d9f6b8c5-xk2lm   1/1   Running' },
    { k: 'out', t: 'demo-api-7d9f6b8c5-9pq4z   0/1   CrashLoopBackOff  (x18)' },
    { k: 'tool', t: 'kubectl get events -n demo --tail 5' },
    { k: 'out', t: 'Back-off restarting failed container demo-api…' },
    { k: 'esc', t: 'escalate → formal diagnosis · run_01J8ZK7Q' },
    { k: 'empty', t: '' },
    { k: 'rep', t: 'Report · run_01J8ZK7Q ──────────────────────', cls: 'd' },
    { k: 'rep', t: 'Root cause · supported ✓ (3 evidence)', cls: 'v' },
    { k: 'rep', t: 'ConfigMap "demo-api-config" is missing key' },
    { k: 'rep', t: '"config.yaml"; the container crash-loops waiting' },
    { k: 'rep', t: 'for its mount (CreateContainerConfigError ×18).' },
    { k: 'rep', t: 'evidence · e_0191 describe · e_0192 configmap', cls: 'd' },
    { k: 'rep', t: 'next · restore the key, rollout restart', cls: 'd' },
    { k: 'empty', t: '' },
    { k: 'you', t: 'how do you know it is not the image?' },
    { k: 'note', t: 're-reading run_01J8ZK7Q from ledger · e_0191' },
    { k: 'ai', t: 'The image pull succeeded (e_0191) — the container' },
    { k: 'ai', t: 'starts, then exits on the missing file. Not a registry issue.' }
  ];

  /* ── engine + overlay wiring ────────────────────────────── */

  var engine = null;
  var overlay = document.getElementById('overlay');
  var tickerEl = document.getElementById('ticker');
  var stack = document.getElementById('stack');
  var chips = [];        // live chips for current incident
  var stampEl = null;
  var cards = [];        // docked report cards (max 3)
  var liveCard = null;   // card currently floating
  var tickerHideTimer = 0;

  function clampX(x, w) { return Math.max(12, Math.min(window.innerWidth - w - 12, x)); }
  function clampY(y, h) { return Math.max(64, Math.min(window.innerHeight - h - 24, y)); }

  function setTicker(text, kind) {
    if (!tickerEl) return;
    clearTimeout(tickerHideTimer);
    tickerEl.textContent = text;
    tickerEl.className = 'show' + (kind ? ' t-' + kind : '');
  }
  function hideTicker() {
    if (!tickerEl) return;
    tickerHideTimer = setTimeout(function () { tickerEl.className = ''; }, 2600);
  }

  var hooks = {
    onReady: function () { revealHero(); },
    onPhase: function (phase, data) {
      if (phase === 'signal') setTicker(data.ticker, 'red');
      if (phase === 'respond') setTicker('aruing investigating · ' + data.id.replace(/-/g, ' '), 'gold');
    },
    onEvidence: function (i, data, pos) {
      if (!overlay) return;
      var ev = data.evidence[i];
      var el = document.createElement('div');
      el.className = 'chip';
      var code = document.createElement('code');
      code.textContent = '$ ' + ev[0];
      var sub = document.createElement('span');
      sub.className = 'sub';
      sub.textContent = ev[1] + ' · captured';
      el.appendChild(code); el.appendChild(sub);
      var side = pos.x < window.innerWidth * 0.55 ? 1 : -1;
      var cw = 330;
      var x = side > 0 ? pos.x + 52 : pos.x - 52 - cw;
      var y = pos.y - 16 - i * 58;
      el.style.left = clampX(x, cw) + 'px';
      el.style.top = clampY(y, 58) + 'px';
      overlay.appendChild(el);
      chips.push(el);
    },
    onVerdict: function (data, pos) {
      if (!overlay) return;
      setTicker('✓ resolved · report filed', 'green');
      var el = document.createElement('div');
      el.className = 'stamp';
      el.textContent = 'supported ✓ · ' + data.evidence.length + ' evidence';
      el.style.left = clampX(pos.x - 90, 200) + 'px';
      el.style.top = clampY(pos.y - 64, 40) + 'px';
      overlay.appendChild(el);
      stampEl = el;
    },
    onReport: function (data, pos) {
      if (!overlay) return;
      var el = document.createElement('div');
      el.className = 'rcard';
      var head = document.createElement('div');
      head.className = 'rc-head';
      head.textContent = data.report.title;
      var root = document.createElement('p');
      root.className = 'rc-root';
      root.textContent = data.report.root;
      var ver = document.createElement('p');
      ver.className = 'rc-verdict';
      ver.textContent = data.report.verdict;
      var fix = document.createElement('p');
      fix.className = 'rc-fix';
      var b = document.createElement('b'); b.textContent = 'next';
      fix.appendChild(b);
      fix.appendChild(document.createTextNode(data.report.fix));
      el.appendChild(head); el.appendChild(root); el.appendChild(ver); el.appendChild(fix);
      var cw = 300, ch = 150;
      var side = pos.x < window.innerWidth * 0.55 ? 1 : -1;
      var x = side > 0 ? pos.x + 46 : pos.x - 46 - cw;
      var y = pos.y - 30;
      el.style.left = clampX(x, cw) + 'px';
      el.style.top = clampY(y, ch) + 'px';
      overlay.appendChild(el);
      liveCard = el;
    },
    onDock: function () {
      // chips & stamp dissolve
      chips.forEach(function (c) { c.classList.add('bye'); });
      chips = [];
      if (stampEl) { stampEl.classList.add('bye'); stampEl = null; }
      hideTicker();
      if (liveCard) {
        cards.push(liveCard);
        if (cards.length > 3) {
          var old = cards.shift();
          old.classList.add('bye');
          (function (o) { setTimeout(function () { o.remove(); }, 400); })(old);
        }
        liveCard = null;
        layoutStack();
      }
    },
    onCancel: function () {
      // canvas rebuilt mid-incident: clear chrome
      chips.forEach(function (c) { c.remove(); }); chips = [];
      if (stampEl) { stampEl.remove(); stampEl = null; }
      if (liveCard) { liveCard.remove(); liveCard = null; }
      if (tickerEl) tickerEl.className = '';
    }
  };

  function layoutStack() {
    var vh = window.innerHeight;
    for (var i = 0; i < cards.length; i++) {
      var c = cards[i];
      var slot = cards.length - 1 - i; // newest at the bottom
      c.classList.add('docked');
      c.style.left = '16px';
      c.style.top = (vh - 168 - slot * 126) + 'px';
    }
  }
  window.addEventListener('resize', function () { layoutStack(); });

  /* ── hero reveal ────────────────────────────────────────── */
  var heroDone = false;
  function revealHero() {
    if (heroDone) return;
    heroDone = true;
    var els = document.querySelectorAll('.r1, .r2, .r3, .r4, .r5');
    for (var i = 0; i < els.length; i++) {
      (function (el, i) {
        setTimeout(function () { el.classList.add('in'); }, 120 + i * 140);
      })(els[i], i);
    }
    var nav = document.getElementById('nav');
    if (nav) nav.classList.add('in');
  }

  /* ── boot typing ────────────────────────────────────────── */
  function typeBoot(done) {
    var pre = document.getElementById('bootLines');
    var caret = document.createElement('span');
    caret.className = 'caret';
    pre.appendChild(caret);
    var li = 0;

    function nextLine() {
      if (li >= BOOT.length) { setTimeout(done, 420); return; }
      var L = BOOT[li++];
      var line = document.createElement('span');
      line.className = 'bl-' + L.k.split(' ')[0];
      pre.insertBefore(line, caret);
      pre.insertBefore(document.createTextNode('\n'), caret);
      if (L.k === 'cmd') {
        var ci = 0;
        (function typeChar() {
          if (ci < L.t.length) {
            line.textContent += L.t.charAt(ci++);
            setTimeout(typeChar, 13 + Math.random() * 22);
          } else {
            setTimeout(nextLine, 150);
          }
        })();
      } else {
        line.textContent = L.t;
        setTimeout(nextLine, 210);
      }
    }
    nextLine();
  }

  function runEntry() {
    if (REDUCED || !document.getElementById('boot')) {
      revealHero();
      return;
    }
    var boot = document.getElementById('boot');
    // let the terminal panel land before the first keystroke
    setTimeout(function () {
      typeBoot(function () {
      // seed position = last line of the terminal
      var pre = document.getElementById('bootLines');
      var range = document.createRange();
      range.selectNodeContents(pre);
      var rect = range.getBoundingClientRect();
      var sx = rect.left + Math.min(160, rect.width * 0.3);
      var sy = rect.top + Math.max(14, rect.height - 16);
      boot.classList.add('away');
      setTimeout(function () { boot.style.display = 'none'; }, 600);
      if (engine) engine.startGenesis(sx, sy);
      });
    }, 750);
  }

  /* ── engine boot ────────────────────────────────────────── */
  try {
    var stage = document.getElementById('stage');
    if (stage) {
      engine = new ClusterEngine(stage, {
        incidents: INCIDENTS,
        hooks: hooks
      });
      engine.start();
    }
  } catch (err) {
    revealHero(); // graceful: content without the organism
  }

  if (REDUCED && engine) { engine.calm = true; }
  runEntry();

  /* ── 3D viewer controls: drag to orbit, wheel to zoom ── */
  (function () {
    if (!engine) return;
    var dragging = false, lastX = 0, lastY = 0;
    function interactive(t) {
      return !!(t && t.closest && t.closest('a, button, input, textarea, select, .t-body'));
    }
    window.addEventListener('mousedown', function (e) {
      if (e.button !== 0 || interactive(e.target)) return;
      dragging = true;
      lastX = e.clientX; lastY = e.clientY;
      document.body.classList.add('dragging');
      engine.hoverEnabled = false; // tooltips off while orbiting
      e.preventDefault(); // orbit instead of text selection
    });
    window.addEventListener('mousemove', function (e) {
      if (!dragging) return;
      engine.orbitBy(e.clientX - lastX, e.clientY - lastY);
      lastX = e.clientX; lastY = e.clientY;
    });
    window.addEventListener('mouseup', function () {
      dragging = false;
      document.body.classList.remove('dragging');
      engine.hoverEnabled = true;
    });
    window.addEventListener('wheel', function (e) {
      // the home page doesn't scroll — the wheel belongs to the camera
      e.preventDefault();
      engine.zoomBy(e.deltaY > 0 ? 1.1 : 0.9);
    }, { passive: false });
    window.addEventListener('dblclick', function (e) {
      if (interactive(e.target)) return;
      engine.resetView();
    });
  })();

  /* ── node tooltips: hover a pod → its service dossier ── */
  (function () {
    var tip = document.getElementById('nodeTip');
    if (!tip || !engine) return;
    var lastIdx = -2, since = 0;
    function statusOf(c) {
      if (c.state === 'sick') return ['CrashLoopBackOff', 't-bad'];
      if (c.state === 'warn') return ['Warning · 503s', 't-warn'];
      if (c.state === 'healing') return ['Recovering…', 't-warn'];
      return ['Running · 1/1', 't-ok'];
    }
    setInterval(function () {
      var idx = engine.hoverEnabled ? engine.hoverIdx : -1;
      if (idx !== lastIdx) { lastIdx = idx; since = performance.now(); }
      var settled = performance.now() - since > 180;
      if (idx >= 0 && settled) {
        var c = engine.cells[idx];
        var g = engine.groups[c.group];
        var m = c.meta || { pod: '?', age: '?', restarts: 0 };
        var gm = g.meta || { svc: '?', ns: '?', replicas: '?' };
        var st = statusOf(c);
        var html = '<div class="t-svc">' + gm.svc + ' <span class="t-dim">· ns/' + gm.ns + '</span></div>' +
          '<div class="t-dim">deployment · ' + gm.replicas + ' replicas</div>' +
          '<div class="t-sep"></div>' +
          '<div>pod ' + m.pod + '</div>' +
          '<div class="' + st[1] + '">' + st[0] + ' · ' + m.age + ' · ' + m.restarts + ' restarts</div>';
        if (c.memory) html += '<div class="t-ok">✓ diagnosed by aruing</div>';
        tip.innerHTML = html;
        var pos = engine.cellPos(idx);
        var r = (c.r * c.sd || 8) + 14;
        var x = pos.x + r;
        if (x + 300 > window.innerWidth) x = pos.x - r - 292;
        x = Math.max(8, Math.min(window.innerWidth - 300, x));
        var y = Math.max(60, Math.min(window.innerHeight - 170, pos.y - 14));
        tip.style.left = x + 'px';
        tip.style.top = y + 'px';
        tip.classList.add('show');
      } else {
        tip.classList.remove('show');
      }
    }, 60);
  })();

  /* ── hero presence (home): attention = presence.
     Any input — or resting the pointer on the copy — keeps the hero on
     screen; after 10s of total stillness (with a 12s post-reveal grace)
     it dissolves so the stage can breathe, and any activity brings it
     back. The view hint retires after the first camera interaction. ── */
  (function () {
    var heroInner = document.querySelector('.hero-inner');
    var hint = document.querySelector('.view-hint');
    if (!heroInner || !engine) return;
    var revealAt = 0, hidden = false, hintRetired = false;
    var lastActivity = performance.now();
    var lastMouse = { x: -1, y: -1 };
    ['mousemove', 'mousedown', 'wheel', 'keydown', 'touchstart'].forEach(function (ev) {
      window.addEventListener(ev, function (e) {
        lastActivity = performance.now();
        if (e && typeof e.clientX === 'number') { lastMouse.x = e.clientX; lastMouse.y = e.clientY; }
      }, { passive: true });
    });
    function armHint() {
      if (hintRetired || !hint) return;
      hintRetired = true;
      setTimeout(function () { hint.classList.add('retired'); }, 1500);
    }
    window.addEventListener('mousedown', armHint);
    window.addEventListener('wheel', armHint);
    setInterval(function () {
      if (!revealAt) {
        if (document.querySelector('.r2.in')) revealAt = performance.now();
        return;
      }
      var now = performance.now();
      var r = heroInner.getBoundingClientRect();
      var overCopy = lastMouse.x >= r.left && lastMouse.x <= r.right &&
                     lastMouse.y >= r.top && lastMouse.y <= r.bottom;
      var idle = (now - lastActivity > 6000) && !overCopy;
      var graceOver = now - revealAt > 8000;
      if (!hidden && idle && graceOver) {
        hidden = true;
        document.body.classList.add('hero-hidden');
      } else if (hidden && !idle) {
        hidden = false;
        document.body.classList.remove('hero-hidden');
      }
    }, 250);
  })();

  /* ── terminal theater ───────────────────────────────────── */
  var theaterPlayed = false;
  var playToken = 0;

  function groupChat() {
    var out = [];
    var i = 0;
    while (i < CHAT.length) {
      if (CHAT[i].k === 'rep') {
        var box = { k: 'repbox', lines: [] };
        while (i < CHAT.length && CHAT[i].k === 'rep') {
          box.lines.push(CHAT[i]); i++;
        }
        out.push(box);
      } else {
        out.push(CHAT[i]); i++;
      }
    }
    return out;
  }

  function playTheater() {
    var body = document.getElementById('chatBody');
    var token = ++playToken;
    body.innerHTML = '';
    var script = groupChat();
    var i = 0;

    function alive() { return token === playToken; }

    function scroll() { body.scrollTop = body.scrollHeight; }

    function next() {
      if (!alive() || i >= script.length) return;
      var L = script[i++];
      if (L.k === 'repbox') {
        var box = document.createElement('div');
        box.className = 't-report';
        body.appendChild(box);
        var j = 0;
        (function nextRep() {
          if (!alive() || j >= L.lines.length) { setTimeout(next, 300); return; }
          var rl = document.createElement('div');
          rl.className = 'tl l-rep' + (L.lines[j].cls ? ' ' + L.lines[j].cls : '');
          rl.textContent = L.lines[j++].t;
          box.appendChild(rl);
          scroll();
          setTimeout(nextRep, 70);
        })();
        return;
      }
      var el = document.createElement('div');
      el.className = 'tl l-' + L.k;
      body.appendChild(el);
      if (L.k === 'cmd' || L.k === 'you') {
        var ci = 0;
        (function typeChar() {
          if (!alive()) return;
          if (ci < L.t.length) {
            el.textContent += L.t.charAt(ci++);
            scroll();
            setTimeout(typeChar, 11 + Math.random() * 18);
          } else {
            scroll();
            setTimeout(next, 320);
          }
        })();
      } else {
        el.textContent = L.t;
        scroll();
        setTimeout(next, L.k === 'out' ? 110 : 260);
      }
    }
    next();
  }

  var rp = document.getElementById('replay');
  if (rp) rp.addEventListener('click', playTheater);

  if ('IntersectionObserver' in window) {
    var theaterEl = document.querySelector('.theater');
    if (theaterEl) {
      var theaterObs = new IntersectionObserver(function (entries) {
        entries.forEach(function (en) {
          if (en.isIntersecting && !theaterPlayed) {
            theaterPlayed = true;
            REDUCED ? renderTheaterStatic() : playTheater();
            theaterObs.disconnect();
          }
        });
      }, { threshold: 0.3 });
      theaterObs.observe(theaterEl);
    }

    var revealObs = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting) {
          en.target.classList.add('in');
          revealObs.unobserve(en.target);
        }
      });
    }, { threshold: 0.12 });
    document.querySelectorAll('.reveal').forEach(function (el) { revealObs.observe(el); });
  } else {
    document.querySelectorAll('.reveal').forEach(function (el) { el.classList.add('in'); });
    if (document.querySelector('.theater')) renderTheaterStatic();
  }

  function renderTheaterStatic() {
    var body = document.getElementById('chatBody');
    body.innerHTML = '';
    groupChat().forEach(function (L) {
      if (L.k === 'repbox') {
        var box = document.createElement('div');
        box.className = 't-report';
        L.lines.forEach(function (rl) {
          var d = document.createElement('div');
          d.className = 'tl l-rep' + (rl.cls ? ' ' + rl.cls : '');
          d.textContent = rl.t;
          box.appendChild(d);
        });
        body.appendChild(box);
      } else {
        var el = document.createElement('div');
        el.className = 'tl l-' + L.k;
        el.textContent = L.t;
        body.appendChild(el);
      }
    });
  }

  /* ── copy button ────────────────────────────────────────── */
  var copyBtn = document.getElementById('copy');
  if (copyBtn) copyBtn.addEventListener('click', function () {
    var btn = this;
    var text = document.getElementById('installCmd').textContent.trim();
    function finish(ok) {
      btn.textContent = ok ? 'copied ✓' : 'failed';
      btn.classList.toggle('done', ok);
      setTimeout(function () { btn.textContent = 'copy'; btn.classList.remove('done'); }, 1700);
    }
    function fallback() {
      try {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed'; ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        var ok = document.execCommand('copy');
        document.body.removeChild(ta);
        return ok;
      } catch (e) { return false; }
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(
        function () { finish(true); },
        function () { finish(fallback()); }
      );
    } else {
      finish(fallback());
    }
  });
})();
