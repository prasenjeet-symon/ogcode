package master

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// The console chrome: one shared design system (consoleCSS) plus a fixed top
// bar rendered around every operator page. Each page template is still a
// standalone html/template — the chrome is a plain string the page source is
// wrapped in before parsing, so page bodies keep writing plain {{...}} actions
// without a nested-template data contract.

// Nav keys identifying the active top-bar item.
const (
	navDashboard = "dashboard"
	navSessions  = "sessions"
	navRepos     = "repos"
	navUsers     = "users"
)

// consoleVersion is the small version tag shown next to the brand.
const consoleVersion = "v0.34"

// consoleCSS is the shared stylesheet for every console surface. One design
// system — an "operator OS" console: near-black ground, monospace accents for
// ids/emails/telemetry, a green health accent, avatar tiles, and status pills.
// Theme-aware: the light palette lives on bare :root and the dark palette
// overrides it under prefers-color-scheme, so the console follows the OS setting.
var consoleCSS = `
:root{
  color-scheme:light dark;
  --bg:#f5f6f9; --panel:#ffffff; --card:#ffffff; --card2:#f1f3f6;
  --border:#e6e8ee; --border2:#d6dae2;
  --text:#161a22; --muted:#5c6472; --faint:#9096a2;
  --accent:#5b63d6; --accent-hover:#4a51c4; --accent-soft:rgba(91,99,214,.10);
  --accent-text:#4a51c4; --on-accent:#ffffff;
  --ok:#0c7a41; --ok-bg:rgba(12,140,70,.10); --ok-border:rgba(12,140,70,.26); --ok-solid:#16a34a;
  --warn:#8a6100; --warn-bg:rgba(190,140,20,.13); --warn-border:rgba(190,140,20,.28);
  --err:#c22c2c; --err-bg:rgba(194,44,44,.09); --err-border:rgba(194,44,44,.26);
  --shadow:0 1px 2px rgba(20,24,33,.04),0 2px 6px rgba(20,24,33,.05);
  --shadow-lg:0 14px 40px rgba(20,24,33,.16);
  --radius:12px; --radius-s:9px; --radius-xs:6px;
  --mono:ui-monospace,"SF Mono",SFMono-Regular,Menlo,"Cascadia Code","Roboto Mono",monospace;
  --sans:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Inter,"Helvetica Neue",Arial,sans-serif;
}
@media (prefers-color-scheme:dark){
  :root{
    --bg:#0a0b0e; --panel:#0e0f13; --card:#111318; --card2:#171a20;
    --border:#1f232b; --border2:#2b303a;
    --text:#e7e9ee; --muted:#8b909c; --faint:#5a616d;
    --accent:#6b74e8; --accent-hover:#7d86ef; --accent-soft:rgba(107,116,232,.16);
    --accent-text:#aab0f7; --on-accent:#ffffff;
    --ok:#4ade80; --ok-bg:rgba(74,222,128,.10); --ok-border:rgba(74,222,128,.22); --ok-solid:#22c55e;
    --warn:#e8c878; --warn-bg:rgba(224,176,80,.12); --warn-border:rgba(224,176,80,.24);
    --err:#f2a5a5; --err-bg:rgba(220,80,80,.12); --err-border:rgba(220,80,80,.28);
    --shadow:0 1px 2px rgba(0,0,0,.45); --shadow-lg:0 20px 54px rgba(0,0,0,.6);
  }
}
*{box-sizing:border-box}
html,body{margin:0;padding:0}
body{min-height:100vh;font:14px/1.55 var(--sans);background:var(--bg);color:var(--text);
  -webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility}
a{color:var(--accent-text);text-decoration:none}
a:hover{text-decoration:underline}
::selection{background:var(--accent-soft)}

/* ---- Top bar ------------------------------------------------------------ */
.topbar{position:sticky;top:0;z-index:40;display:flex;align-items:center;gap:16px;height:57px;
  padding:0 20px;background:color-mix(in srgb,var(--panel) 88%,transparent);
  backdrop-filter:saturate(140%) blur(8px);border-bottom:1px solid var(--border)}
.tb-brand{display:flex;align-items:center;gap:9px;color:var(--text);font-weight:670;font-size:15px;letter-spacing:-.01em;flex-shrink:0}
.tb-brand:hover{text-decoration:none}
.tb-brand svg{width:22px;height:22px;color:var(--accent)}
.tb-brand .ver{font-family:var(--mono);font-size:10px;color:var(--muted);background:var(--card2);
  border:1px solid var(--border);border-radius:5px;padding:1px 6px;font-weight:500}
.tb-nav{display:flex;align-items:center;gap:2px;overflow-x:auto;scrollbar-width:none}
.tb-nav::-webkit-scrollbar{display:none}
.tb-nav a{display:flex;align-items:center;gap:8px;padding:7px 12px;border-radius:8px;color:var(--muted);
  font-size:13.5px;font-weight:500;white-space:nowrap}
.tb-nav a svg{width:16px;height:16px;opacity:.85}
.tb-nav a:hover{background:var(--card2);color:var(--text);text-decoration:none}
.tb-nav a.active{background:var(--accent-soft);color:var(--accent-text);font-weight:600}
.tb-nav a.active svg{color:var(--accent-text);opacity:1}
.tb-spacer{flex:1}
.tb-op{display:flex;align-items:center;gap:10px;flex-shrink:0}
.tb-op .op-email{font-family:var(--mono);font-size:12.5px;color:var(--muted);max-width:180px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.tb-signout{display:grid;place-items:center;width:34px;height:34px;border-radius:var(--radius-xs);
  border:1px solid var(--border);color:var(--muted);background:var(--card)}
.tb-signout:hover{background:var(--card2);color:var(--text);text-decoration:none}
.tb-signout svg{width:17px;height:17px}
.avatar{width:32px;height:32px;border-radius:9px;display:grid;place-items:center;font-family:var(--mono);
  font-size:11.5px;font-weight:700;color:#fff;flex-shrink:0;position:relative}
.avatar.sm{width:28px;height:28px;border-radius:8px;font-size:10.5px}
.av0{background:linear-gradient(140deg,#2dd4bf,#0e7490)}
.av1{background:linear-gradient(140deg,#f59e0b,#b45309)}
.av2{background:linear-gradient(140deg,#818cf8,#4338ca)}
.av3{background:linear-gradient(140deg,#f472b6,#9d174d)}
.av4{background:linear-gradient(140deg,#34d399,#15803d)}
.av5{background:linear-gradient(140deg,#38bdf8,#0369a1)}
.avatar .dot{position:absolute;right:-3px;bottom:-3px;width:11px;height:11px;border-radius:50%;
  background:var(--ok-solid);border:2px solid var(--card)}

/* ---- Page container + header + footer ----------------------------------- */
.page{max-width:1180px;margin:0 auto;padding:34px 28px 26px}
.phead{display:flex;align-items:center;justify-content:space-between;gap:14px 22px;margin-bottom:14px;flex-wrap:wrap}
.phead h1{margin:0;font-size:24px;font-weight:720;letter-spacing:-.025em;display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.phead .sub{margin:8px 0 0;color:var(--muted);font-size:13.5px;max-width:64ch}
.phead-actions{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.phead-actions button,.phead-actions .btn{margin-top:0}
.page-title{margin:0 0 6px;font-size:24px;font-weight:720;letter-spacing:-.025em}
.page-sub{margin:0 0 26px;color:var(--muted);font-size:13.5px;max-width:66ch}
.statuspill{display:inline-flex;align-items:center;gap:7px;font-size:12px;font-weight:600;padding:4px 11px;
  border-radius:999px;background:var(--ok-bg);color:var(--ok);border:1px solid var(--ok-border);font-family:var(--sans)}
.statuspill::before{content:"";width:7px;height:7px;border-radius:50%;background:currentColor;box-shadow:0 0 0 3px color-mix(in srgb,currentColor 22%,transparent)}
.seg{display:inline-flex;background:var(--card2);border:1px solid var(--border);border-radius:9px;padding:3px}
.seg a{padding:6px 13px;border-radius:6px;font-size:13px;line-height:1.2;color:var(--muted);display:flex;gap:7px;align-items:center;font-weight:500}
.seg a:hover{text-decoration:none;color:var(--text)}
.seg a .n{font-family:var(--mono);font-size:11px;color:var(--faint)}
.seg a.active{background:var(--card);color:var(--text);box-shadow:var(--shadow)}
.seg a.active .n{color:var(--accent-text)}
.pagefoot{max-width:1180px;margin:0 auto;padding:16px 28px 34px;display:flex;justify-content:space-between;
  gap:16px;flex-wrap:wrap;border-top:1px solid var(--border);color:var(--faint);font-family:var(--mono);font-size:11.5px}
.pagefoot .fl{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.pagefoot b{color:var(--muted);font-weight:600;letter-spacing:.04em}
.pagefoot .links{display:flex;gap:18px}
.pagefoot .links span{color:var(--muted)}

/* ---- Cards -------------------------------------------------------------- */
.card{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);
  padding:20px 22px;margin-bottom:18px;box-shadow:var(--shadow)}
.card h2{margin:0 0 16px;font-size:14px;font-weight:640;letter-spacing:-.01em;color:var(--text)}
.card-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin:0 0 16px}
.card-head h2{margin:0}
/* Edge-to-edge panel: a bordered container whose table/header/footer are flush. */
.panel{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);
  box-shadow:var(--shadow);overflow:hidden;margin-bottom:18px}
.panel table{width:100%;border-collapse:collapse;font-size:13.5px}
.panel th{padding:15px 24px;color:var(--faint);font-size:10.5px;font-weight:700;text-transform:uppercase;
  letter-spacing:.07em;text-align:left;border-bottom:1px solid var(--border);background:color-mix(in srgb,var(--card2) 55%,transparent)}
.panel td{padding:16px 24px;border-bottom:1px solid var(--border);vertical-align:middle}
.panel tr:last-child td{border-bottom:none}
.panel tbody tr:hover td,.panel table tr:hover td{background:color-mix(in srgb,var(--card2) 55%,transparent)}
.cardfoot{display:flex;justify-content:space-between;gap:16px;flex-wrap:wrap;padding:14px 24px;
  border-top:1px solid var(--border);color:var(--faint);font-family:var(--mono);font-size:11.5px;
  background:color-mix(in srgb,var(--card2) 40%,transparent)}
.cardfoot .live{color:var(--muted)}
.cardfoot .live::before{content:"";display:inline-block;width:6px;height:6px;border-radius:50%;
  background:var(--ok-solid);margin-right:7px;vertical-align:middle}

/* ---- Flash banners ------------------------------------------------------ */
.flash{margin-bottom:20px;padding:12px 15px;border-radius:var(--radius-s);font-size:13px;
  word-break:break-word;display:flex;gap:9px;align-items:flex-start;line-height:1.45}
.flash::before{font-weight:700;flex-shrink:0}
.flash.ok{background:var(--ok-bg);color:var(--ok);border:1px solid var(--ok-border)}
.flash.ok::before{content:"\2713"}
.flash.err{background:var(--err-bg);color:var(--err);border:1px solid var(--err-border)}
.flash.err::before{content:"\26A0"}

/* ---- Forms -------------------------------------------------------------- */
label{display:block;font-size:12px;font-weight:550;color:var(--muted);margin:14px 0 6px}
input[type=text],input[type=password],input[type=email],select,textarea{width:100%;background:var(--bg);
  border:1px solid var(--border2);border-radius:var(--radius-s);padding:9px 12px;
  color:var(--text);font-size:13.5px;font-family:var(--sans);transition:border-color .12s,box-shadow .12s}
select{appearance:none;-webkit-appearance:none;-moz-appearance:none;cursor:pointer;padding-right:36px;
  background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%235c6472' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M6 9l6 6 6-6'/%3E%3C/svg%3E");
  background-repeat:no-repeat;background-position:right 12px center;background-size:15px}
@media (prefers-color-scheme:dark){select{background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%238b909c' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M6 9l6 6 6-6'/%3E%3C/svg%3E")}}
textarea{resize:vertical;min-height:88px;line-height:1.5}
input::placeholder,textarea::placeholder{color:var(--faint)}
input:hover:not(:focus),select:hover:not(:focus),textarea:hover:not(:focus){border-color:var(--faint)}
input:focus,select:focus,textarea:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)}
.hint{color:var(--muted);font-size:12px;margin-top:6px;line-height:1.5}

/* ---- Buttons ------------------------------------------------------------ */
button,.btn{margin-top:16px;background:var(--accent);color:var(--on-accent);border:1px solid transparent;
  border-radius:var(--radius-s);padding:9px 16px;font-size:13px;font-weight:600;cursor:pointer;
  font-family:var(--sans);transition:background .12s,border-color .12s,color .12s;line-height:1.2;
  display:inline-flex;align-items:center;gap:8px;justify-content:center}
button:hover,.btn:hover{background:var(--accent-hover);text-decoration:none}
button svg,.btn svg{width:15px;height:15px}
button.danger,.btn.danger{background:transparent;color:var(--err);border-color:var(--err-border)}
button.danger:hover,.btn.danger:hover{background:var(--err-bg)}
button.linkish,.btn.linkish{background:transparent;color:var(--text);border-color:var(--border2)}
button.linkish:hover,.btn.linkish:hover{background:var(--card2)}
button.sm,.btn.sm{margin-top:0;padding:6px 12px;font-size:12px;font-weight:550;border-radius:var(--radius-xs)}

/* ---- Tables (plain, inside .card) --------------------------------------- */
table{width:100%;border-collapse:collapse;font-size:13.5px}
th{text-align:left;color:var(--faint);font-size:10.5px;font-weight:700;text-transform:uppercase;
  letter-spacing:.06em;padding:0 10px 10px;border-bottom:1px solid var(--border)}
td{padding:12px 10px;border-bottom:1px solid var(--border);vertical-align:top}
tr:last-child td{border-bottom:none}
td.name{font-weight:600}
td.wide{min-width:220px}
.td-sub{color:var(--muted);font-size:12px;margin-top:3px;word-break:break-all;font-family:var(--mono)}

/* ---- Employee identity + repository chips ------------------------------- */
.emp{display:flex;align-items:center;gap:13px}
.emp-name{font-weight:600;font-size:14px;display:flex;align-items:baseline;gap:8px;flex-wrap:wrap}
.emp-id{font-family:var(--mono);font-size:11.5px;color:var(--faint);font-weight:500}
.emp-email{font-family:var(--mono);font-size:12px;color:var(--muted);margin-top:3px}
.repochip{display:inline-flex;align-items:center;gap:8px;font-family:var(--mono);font-size:12.5px;
  padding:7px 12px;border:1px solid var(--border2);border-radius:9px;color:var(--text);background:var(--card2);
  cursor:pointer;list-style:none}
.repochip::-webkit-details-marker{display:none}
.repochip svg{width:14px;height:14px;color:var(--muted)}
.repochip .caret{color:var(--faint);margin-left:1px}
.repochip:hover{border-color:var(--accent);color:var(--text)}
.repochip.all{color:var(--ok);border-color:var(--ok-border);background:var(--ok-bg)}
.repochip.all svg{color:var(--ok)}
.repochip.none{color:var(--faint);cursor:default}
.repopop{position:relative;display:inline-block}
.repopop[open]>.repochip{border-color:var(--accent);color:var(--text)}

/* ---- Pills, badges, tags ------------------------------------------------ */
.pill{display:inline-flex;align-items:center;gap:5px;font-size:11px;padding:3px 9px;margin:2px 6px 2px 0;
  border:1px solid var(--border2);border-radius:999px;color:var(--muted);background:var(--card2);white-space:nowrap;font-family:var(--mono)}
.pill.admin{border-color:var(--accent-soft);color:var(--accent-text);background:var(--accent-soft)}
.pill.run{background:var(--ok-bg);color:var(--ok);border-color:var(--ok-border)}
.pill.wait{background:var(--warn-bg);color:var(--warn);border-color:var(--warn-border)}
.pill.done{background:var(--accent-soft);color:var(--accent-text);border-color:var(--accent-soft)}
.pill.err{background:var(--err-bg);color:var(--err);border-color:var(--err-border)}
.unrestricted{color:var(--ok);font-size:12px;font-weight:550}
.badge{display:inline-flex;align-items:center;gap:6px;font-size:11px;font-weight:650;padding:3px 10px;
  border-radius:999px;text-transform:capitalize;border:1px solid transparent}
.badge::before{content:"";width:6px;height:6px;border-radius:50%;background:currentColor}
.badge.online{background:var(--ok-bg);color:var(--ok);border-color:var(--ok-border)}
.badge.offline{background:var(--warn-bg);color:var(--warn);border-color:var(--warn-border)}
.badge.dead{background:var(--err-bg);color:var(--err);border-color:var(--err-border)}
.cap{display:inline-block;font-size:11px;padding:2px 9px;margin:3px 6px 0 0;border:1px solid var(--border2);
  border-radius:999px;color:var(--muted);font-family:var(--mono)}
.tags{display:flex;flex-wrap:wrap;gap:6px;align-items:center}
.tag{display:inline-flex;align-items:center;gap:7px;font-size:12px;padding:4px 5px 4px 11px;font-family:var(--mono);
  border:1px solid var(--border2);border-radius:8px;color:var(--text);background:var(--card2);max-width:100%}
.tag code{background:none;border:none;padding:0;font-size:12px;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.tag form{margin:0}
.tag button{margin:0;padding:1px 7px;font-size:12px;line-height:1.2;border-radius:6px;background:transparent;color:var(--muted);border:none}
.tag button:hover{background:var(--err-bg);color:var(--err)}

/* ---- Stat tiles --------------------------------------------------------- */
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:14px;margin-bottom:22px}
.tile{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:18px 20px;box-shadow:var(--shadow)}
a.tile-link{color:inherit;text-decoration:none;display:block;transition:border-color .12s,transform .08s}
a.tile-link:hover{text-decoration:none;border-color:var(--accent);transform:translateY(-1px)}
.tile .num{font-size:30px;font-weight:720;line-height:1.02;letter-spacing:-.03em;font-family:var(--mono)}
.tile .lbl{color:var(--muted);font-size:11.5px;margin-top:7px;font-weight:600;text-transform:uppercase;letter-spacing:.05em}
a.tile-link .lbl{color:var(--accent-text)}

/* ---- Worker cards ------------------------------------------------------- */
.worker-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(330px,1fr));gap:14px}
.worker{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:18px 20px;box-shadow:var(--shadow)}
.worker .top{display:flex;align-items:center;gap:11px}
.worker h2{margin:0;font-size:15px;font-weight:640;flex:1;text-transform:none;letter-spacing:normal;color:var(--text)}
.worker .id{color:var(--muted);font-size:12px;font-family:var(--mono);margin-top:4px}
.worker .meta{color:var(--muted);font-size:12.5px;margin-top:10px;font-family:var(--mono)}
.worker .caps{margin-top:9px}
.worker a.open{display:inline-block;margin-top:14px;margin-right:16px;font-size:13px;font-weight:550}

/* ---- Misc --------------------------------------------------------------- */
.empty{padding:54px 32px;text-align:center;color:var(--muted);border:1px dashed var(--border2);border-radius:var(--radius);background:var(--card)}
.empty svg{width:34px;height:34px;color:var(--faint);margin-bottom:12px}
.empty b{display:block;color:var(--text);font-size:15px;font-weight:600;margin-bottom:5px}
.kv{display:flex;align-items:baseline;gap:10px;margin:8px 0;font-size:13px}
.kv>span:first-child{color:var(--muted);min-width:78px}
.kv a{margin-right:12px}
code{font-family:var(--mono);font-size:12.5px;background:var(--card2);border:1px solid var(--border);border-radius:var(--radius-xs);padding:2px 6px}
.muted{color:var(--muted);font-size:12px}
.dot{display:inline-block;width:8px;height:8px;border-radius:50%;background:var(--warn);margin-right:6px;vertical-align:middle}
.dot.live{background:var(--ok-solid)}

/* ---- Event feed (session monitor) --------------------------------------- */
ul.feed{list-style:none;margin:0;padding:0;max-height:440px;overflow-y:auto}
ul.feed li{border-left:2px solid var(--border2);padding:6px 0 6px 13px;margin-bottom:9px}
ul.feed li.placeholder{color:var(--muted);font-size:13px;border-left-color:transparent}
.evt-head{display:flex;align-items:baseline;gap:10px}
.evt-type{font-family:var(--mono);font-size:13px;color:var(--accent-text)}
.evt-seq{font-family:var(--mono);font-size:11px;color:var(--faint)}
ul.feed pre{margin:3px 0 0;font-size:12px;line-height:1.5;color:var(--muted);white-space:pre-wrap;word-break:break-word;font-family:var(--mono)}

/* ---- Form layout helpers ------------------------------------------------ */
.frow{display:grid;gap:16px;grid-template-columns:1fr 1fr}
.frow label{margin-top:0}
@media(max-width:760px){.frow{grid-template-columns:1fr}}
.form-card{flex:1 1 320px;min-width:280px}
.form-cols{display:flex;flex-wrap:wrap;gap:18px;align-items:flex-start}
.form-cols .form-card{margin-bottom:0}
form.inline{display:flex;align-items:center;gap:9px;margin:0}
.checks{display:flex;flex-wrap:wrap;gap:10px 18px;margin-top:8px}
.checks label{margin:0;display:inline-flex;gap:8px;align-items:center;font-size:13px;font-weight:450;color:var(--text);cursor:pointer}
.checks input{width:auto}
.row-actions{display:flex;flex-wrap:wrap;gap:8px;justify-content:flex-end;align-items:center}
.row-actions form{margin:0;display:flex;align-items:center;gap:7px}
.row-actions input[type=text]{width:auto;padding:6px 9px;font-size:12px}
.row-actions label.chk{margin:0;display:inline-flex;align-items:center;gap:4px;font-size:12px;color:var(--muted)}
/* Row action menu: a kebab summary that opens an absolutely-positioned popover. */
.rowmenu{position:relative;display:inline-block}
.rowmenu>summary{list-style:none;cursor:pointer;width:32px;height:32px;border-radius:var(--radius-xs);
  border:1px solid var(--border2);display:grid;place-items:center;color:var(--muted);background:var(--card);
  transition:background .12s,color .12s,border-color .12s}
.rowmenu>summary::-webkit-details-marker{display:none}
.rowmenu>summary svg{width:18px;height:18px}
.rowmenu>summary:hover{background:var(--card2);color:var(--text)}
.rowmenu[open]>summary{background:var(--accent-soft);color:var(--accent-text);border-color:var(--accent)}
.rowmenu-pop{position:absolute;right:0;top:calc(100% + 6px);z-index:50;width:272px;
  background:var(--card);border:1px solid var(--border2);border-radius:var(--radius-s);box-shadow:var(--shadow-lg);overflow:hidden}
.rowmenu-pop.wide{width:320px}
.rowmenu-sec{padding:12px 14px}
.rowmenu-sec+.rowmenu-sec{border-top:1px solid var(--border)}
.rowmenu-sec .t{font-size:10.5px;font-weight:700;letter-spacing:.05em;text-transform:uppercase;color:var(--faint);margin-bottom:9px}
.rowmenu-sec form{margin:0;display:flex;flex-wrap:wrap;gap:8px;align-items:center}
.rowmenu-sec input[type=text]{flex:1 1 90px;min-width:0;padding:6px 9px;font-size:12px}
.rowmenu-sec button{margin-top:0;width:100%}
.rowmenu-sec .inline-row{display:flex;gap:8px;align-items:center;width:100%}
.rowmenu-sec .inline-row button{width:auto;flex:1}
.rowmenu-sec label.chk{margin:0;display:inline-flex;align-items:center;gap:6px;font-size:12px;color:var(--muted)}
/* A plain action-list popover: full-width menu items, one action each. */
.rowmenu-pop.menu{width:232px;padding:6px}
.rowmenu-pop.menu form{margin:0}
.menu-item{width:100%;margin:0;background:transparent;border:none;border-radius:var(--radius-xs);
  padding:9px 10px;display:flex;align-items:center;gap:10px;justify-content:flex-start;text-align:left;
  color:var(--text);font-size:13px;font-weight:500;font-family:var(--sans);cursor:pointer}
.menu-item:hover{background:var(--card2)}
.menu-item svg{width:16px;height:16px;color:var(--muted);flex-shrink:0}
.menu-item.danger{color:var(--err)}
.menu-item.danger:hover{background:var(--err-bg)}
.menu-item.danger svg{color:var(--err)}
.menu-sep{height:1px;background:var(--border);margin:6px 4px}
.menu-info{padding:8px 10px 4px;font-size:11px;color:var(--faint);display:flex;align-items:center;gap:6px;flex-wrap:wrap}
.checks label.assigned{color:var(--muted);cursor:default}
details.card>summary{cursor:pointer;font-size:13.5px;font-weight:600;letter-spacing:-.01em;
  color:var(--text);list-style:none;display:flex;align-items:center;gap:8px}
details.card>summary::-webkit-details-marker{display:none}
details.card>summary::before{content:"\203A";font-size:16px;transition:transform .15s;display:inline-block;color:var(--faint)}
details.card[open]>summary::before{transform:rotate(90deg)}
details.card[open]>summary{margin-bottom:16px}

/* ---- Modal dialog ------------------------------------------------------- */
dialog.modal{border:none;padding:0;border-radius:var(--radius);background:var(--card);color:var(--text);
  box-shadow:var(--shadow-lg);width:min(480px,calc(100vw - 32px));max-width:480px}
dialog.modal::backdrop{background:rgba(5,7,11,.62);backdrop-filter:blur(2px)}
.modal-head{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:17px 22px;border-bottom:1px solid var(--border)}
.modal-head h2{margin:0;font-size:15px;font-weight:650}
.modal-x{margin:0;width:30px;height:30px;padding:0;display:grid;place-items:center;background:transparent;
  border:1px solid var(--border2);color:var(--muted);border-radius:var(--radius-xs)}
.modal-x:hover{background:var(--card2);color:var(--text)}
.modal-x svg{width:15px;height:15px}
.modal-body{padding:20px 22px}
.modal-body>label:first-child{margin-top:0}
.modal-foot{display:flex;justify-content:flex-end;gap:10px;padding:15px 22px;border-top:1px solid var(--border);
  background:color-mix(in srgb,var(--card2) 40%,transparent)}
.modal-foot button{margin-top:0}

/* ---- Responsive --------------------------------------------------------- */
@media(max-width:900px){
  .tb-op .op-email{display:none}
}
@media(max-width:680px){
  .tb-brand .brandname{display:none}
  .page{padding:24px 16px 20px}
}
`

// chromeBrandSVG is the top-bar logo mark (a lightning bolt — the "operator OS").
const chromeBrandSVG = `<svg viewBox="0 0 24 24" fill="currentColor"><path d="M13 2L4.5 13.2c-.4.5 0 1.3.7 1.3H11l-1.6 7.2c-.2.8.9 1.3 1.4.6L19.5 11c.4-.5 0-1.3-.7-1.3H13l1.4-6.9c.2-.9-.9-1.4-1.4-.8z"/></svg>`

// navIconSVGs are the per-item top-bar icons, keyed by nav item.
var navIconSVGs = map[string]string{
	navDashboard: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3.5" y="3.5" width="7" height="7" rx="1.5"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.5"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.5"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.5"/></svg>`,
	navSessions:  `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M7 8l4 4-4 4"/><path d="M13 16h5"/></svg>`,
	navUsers:     `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="9.5" cy="8.5" r="3.2"/><path d="M3.5 19.5c.6-3.1 3.1-4.8 6-4.8s5.4 1.7 6 4.8"/><path d="M16 5.6a3.2 3.2 0 010 5.8"/><path d="M17.8 15c1.7.6 2.9 1.9 3.3 4"/></svg>`,
	navRepos:     `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7.5A2.5 2.5 0 016.5 5H19v14H6.5A2.5 2.5 0 014 16.5z"/><path d="M4 7.5v9"/><path d="M8 9h7M8 12.5h5"/></svg>`,
}

// navItem is one top-bar entry.
type navItem struct {
	key   string
	href  string
	label string
}

// navItems are the console's primary sections, in top-bar order: the operator
// workflow runs Employees → Repositories → Sessions.
var navItems = []navItem{
	{key: navDashboard, href: "/", label: "Dashboard"},
	{key: navUsers, href: operatorUsersPath, label: "Employees"},
	{key: navRepos, href: operatorReposPath, label: "Repositories"},
	{key: navSessions, href: operatorSessionsPath, label: "Sessions"},
}

// avatarClass maps a name to one of the six avatar gradients, deterministically.
func avatarClass(name string) string {
	var sum int
	for _, r := range name {
		sum += int(r)
	}
	return fmt.Sprintf("av%d", sum%6)
}

// initials returns 1–2 uppercase letters for an avatar tile.
func initials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	fields := strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '.' || r == '-' || r == '_' || r == '@' })
	if len(fields) >= 2 && fields[0] != "" && fields[1] != "" {
		return strings.ToUpper(fields[0][:1] + fields[1][:1])
	}
	if len(name) >= 2 {
		return strings.ToUpper(name[:2])
	}
	return strings.ToUpper(name[:1])
}

// consoleTopbarHTML renders the fixed top bar: brand + version, primary nav
// (active item highlighted), a search box, and the signed-in operator.
func consoleTopbarHTML(active, operator, email string, admin bool) string {
	var b strings.Builder
	b.WriteString(`<header class="topbar">`)
	b.WriteString(`<a class="tb-brand" href="/">` + chromeBrandSVG +
		`<span class="brandname">ogcode</span><span class="ver">` + consoleVersion + `</span></a>`)
	b.WriteString(`<nav class="tb-nav">`)
	for _, it := range navItems {
		cls := ""
		if it.key == active {
			cls = ` class="active"`
		} else {
			cls = ` class=""`
		}
		fmt.Fprintf(&b, `<a%s href="%s">%s<span>%s</span></a>`, cls, it.href, navIconSVGs[it.key], it.label)
	}
	b.WriteString(`</nav>`)
	b.WriteString(`<div class="tb-spacer"></div>`)
	if operator != "" {
		disp := email
		if disp == "" {
			disp = operator
		}
		b.WriteString(`<div class="tb-op">` +
			`<span class="avatar ` + avatarClass(operator) + `">` + template.HTMLEscapeString(initials(operator)) + `</span>` +
			`<span class="op-email">` + template.HTMLEscapeString(disp) + `</span></div>`)
	}
	b.WriteString(`<a class="tb-signout" href="` + operatorLogoutPath + `" title="Sign out" aria-label="Sign out">` +
		`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h4.5v16H14"/><path d="M10 8l-4 4 4 4"/><path d="M6 12h9"/></svg></a>`)
	b.WriteString(`</header>`)
	return b.String()
}

// chromeHead opens the shared shell: doctype, head (title + shared stylesheet),
// the top bar, and the page container. The title and operator identity are
// escaped here; nav hrefs and icons are package constants.
func chromeHead(title, active, operator, email string, admin bool) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>` + template.HTMLEscapeString(title) + `</title>`)
	b.WriteString(`<style>` + consoleCSS + `</style></head><body>`)
	b.WriteString(consoleTopbarHTML(active, operator, email, admin))
	b.WriteString(`<main class="page">`)
	return b.String()
}

// chromeFoot closes the page container, renders the operator-OS footer, and
// installs the small scripts (row-menu outside-click close + search filter).
const chromeFoot = `</main>` +
	`<footer class="pagefoot"><div class="fl"><b>OGCODE CONTROL PLANE</b> <span>&middot;</span> <span>Worktree Engine ` + consoleVersion + `</span></div>` +
	`<div class="links"><span>Security</span><span>Audit</span><span>API</span><span>Status</span></div></footer>` +
	`<script>document.addEventListener('click',function(e){document.querySelectorAll('details.rowmenu[open],details.repopop[open]').forEach(function(d){if(!d.contains(e.target))d.removeAttribute('open')})});` +
	`function filterRole(role,el){document.querySelectorAll('.seg a').forEach(function(a){a.classList.remove('active')});el.classList.add('active');document.querySelectorAll('tr[data-role]').forEach(function(tr){tr.style.display=(role==='all'||tr.getAttribute('data-role')===role)?'':'none'});return false}</script>` +
	`</body></html>`

// consoleViewer resolves the signed-in operator for the top bar: the account
// name, its contact email (for the mono identity), and whether the session
// sees every workspace (rendered as the admin role). An empty allowlist is
// exactly the "sees everything" case.
func (s *Server) consoleViewer(r *http.Request) (operator, email string, admin bool) {
	if s.gate == nil {
		return "", "", false
	}
	user, ok := s.gate.UserForSession(r)
	if !ok {
		return "", "", false
	}
	ws, _ := s.gate.WorkspacesForUser(r)
	if store := s.reg.Store(); store != nil {
		if rec, found, err := store.GetUser(user); err == nil && found {
			email = rec.Email
		}
	}
	return user, email, len(ws) == 0
}

// writeChrome writes a page inside the shared shell with the console's standard
// headers (no caching: every surface reflects live state).
func (s *Server) writeChrome(w http.ResponseWriter, r *http.Request, title, active, body string) {
	s.writeChromeStatus(w, r, title, active, body, http.StatusOK)
}

// writeChromeStatus is writeChrome with an explicit HTTP status, for pages
// that render an error state (the session monitor's not-found variant).
func (s *Server) writeChromeStatus(w http.ResponseWriter, r *http.Request, title, active, body string, status int) {
	operator, email, admin := s.consoleViewer(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(chromeHead(title, active, operator, email, admin) + body + chromeFoot))
}
