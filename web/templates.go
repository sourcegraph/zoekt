// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package web

import (
	"html/template"
	"log"
)

// Top provides the standard templates in parsed form
var Top = template.New("top").Funcs(Funcmap)

// TemplateText contains the text of the standard templates.
var TemplateText = map[string]string{
	"head": `
<head>
<meta charset="utf-8">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<meta name="viewport" content="width=device-width, initial-scale=1">
<script>
(function () {
  var t = localStorage.getItem("cs-theme");
  if (t === "light" || t === "dark") {
    document.documentElement.setAttribute("data-theme", t);
  }
})();
</script>
<style>
:root {
  --bg: #ffffff;
  --bg-subtle: #f6f8fa;
  --border: #d1d9e0;
  --fg: #1f2328;
  --fg-muted: #59636e;
  --accent: #0969da;
  --accent-fg: #ffffff;
  --match-bg: #fff8c5;
  --target-bg: #ddf4ff;
  --chip-bg: #eff2f5;
  --shadow: 0 1px 3px rgba(31, 35, 40, 0.06);
  --mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;
  --sans: -apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans", Helvetica, Arial, sans-serif;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --bg: #0d1117;
    --bg-subtle: #151b23;
    --border: #3d444d;
    --fg: #f0f6fc;
    --fg-muted: #9198a1;
    --accent: #4493f8;
    --accent-fg: #ffffff;
    --match-bg: #5a4600;
    --target-bg: #122d4d;
    --chip-bg: #212830;
    --shadow: none;
  }
}
:root[data-theme="dark"] {
  --bg: #0d1117;
  --bg-subtle: #151b23;
  --border: #3d444d;
  --fg: #f0f6fc;
  --fg-muted: #9198a1;
  --accent: #4493f8;
  --accent-fg: #ffffff;
  --match-bg: #5a4600;
  --target-bg: #122d4d;
  --chip-bg: #212830;
  --shadow: none;
}
* { box-sizing: border-box; }
html, body {
  margin: 0;
  padding: 0;
  background: var(--bg);
  color: var(--fg);
  font-family: var(--sans);
  font-size: 14px;
  line-height: 1.5;
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
pre, pre p {
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.45;
  margin: 0;
  background: transparent;
  border: none;
  white-space: pre;
  color: var(--fg);
}
pre b { color: var(--fg); }
pre b { background: var(--match-bg); font-weight: 600; border-radius: 2px; padding: 0 1px; }
.topbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-subtle);
  position: sticky;
  top: 0;
  z-index: 10;
}
.brand {
  font-weight: 600;
  font-size: 15px;
  color: var(--fg) !important;
  margin-right: 4px;
  white-space: nowrap;
}
.brand:hover { text-decoration: none; }
input[type="text"], input[type="number"] {
  background: var(--bg);
  color: var(--fg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px 12px;
  font-size: 14px;
  outline: none;
}
input[type="text"]:focus, input[type="number"]:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 25%, transparent);
}
.topbar input[type="text"] { flex: 1 1 280px; min-width: 180px; font-family: var(--mono); font-size: 13px; }
.numfield { display: flex; align-items: center; gap: 5px; color: var(--fg-muted); font-size: 12px; white-space: nowrap; }
.numfield input { width: 70px; padding: 4px 8px; font-size: 12px; }
button, .btn {
  background: var(--accent);
  color: var(--accent-fg);
  border: 1px solid transparent;
  border-radius: 6px;
  padding: 5px 14px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
}
button:hover { filter: brightness(1.08); }
.btn-ghost {
  background: transparent;
  color: var(--fg-muted);
  border: 1px solid var(--border);
  padding: 4px 9px;
}
.btn-ghost:hover { color: var(--fg); filter: none; background: var(--chip-bg); }
.chip {
  display: inline-block;
  background: var(--chip-bg);
  color: var(--fg-muted);
  border: 1px solid var(--border);
  border-radius: 2em;
  padding: 0 8px;
  font-size: 11px;
  line-height: 18px;
  vertical-align: 1px;
}
button.chip { cursor: pointer; font-weight: 400; }
button.chip:hover { color: var(--accent); filter: none; }
.hero {
  max-width: 640px;
  margin: 9vh auto 0;
  padding: 0 16px;
  text-align: center;
}
.hero h1 { font-size: 22px; font-weight: 600; margin: 0 0 18px; }
.hero form { display: flex; gap: 8px; }
.hero input[type="text"] { flex: 1; font-family: var(--mono); padding: 8px 14px; font-size: 14px; }
.container { max-width: 1012px; margin: 0 auto; padding: 16px; }
.help-cols { display: flex; flex-wrap: wrap; gap: 24px; margin-top: 28px; }
.help-cols > div { flex: 1 1 340px; }
.help-cols h3 { font-size: 14px; font-weight: 600; color: var(--fg-muted); margin: 0 0 8px; }
dl.examples { margin: 0; }
dl.examples dt { font-family: var(--mono); font-size: 12px; float: left; clear: left; margin-right: 10px; }
dl.examples dd { color: var(--fg-muted); font-size: 12px; margin: 0 0 4px 0; overflow: hidden; }
.results-wrap { max-width: 1012px; margin: 0 auto; padding: 12px 16px 40px; }
.result-stats { color: var(--fg-muted); font-size: 13px; margin: 6px 0 14px; }
.result-stats b { color: var(--fg); font-weight: 600; }
.file-card {
  border: 1px solid var(--border);
  border-radius: 6px;
  margin-bottom: 14px;
  box-shadow: var(--shadow);
  overflow: hidden;
}
.file-card-header {
  background: var(--bg-subtle);
  border-bottom: 1px solid var(--border);
  padding: 7px 12px;
  font-size: 13px;
}
.file-card-header a { font-family: var(--mono); font-weight: 600; }
.file-card .snippets { overflow-x: auto; }
.file-card table { border-collapse: collapse; width: 100%; }
.file-card td { border: none; padding: 1px 0; vertical-align: top; }
.lnums {
  width: 1%;
  white-space: nowrap;
  text-align: right;
  padding: 1px 10px !important;
  user-select: none;
}
.lnums pre, .lnums a, .lnums span { color: var(--fg-muted); }
.lnums a:hover { color: var(--accent); }
.noselect { user-select: none; }
.noselect u { text-decoration: none; }
:target { background: var(--target-bg); }
.result { display: block; content: " "; visibility: hidden; }
.footer {
  border-top: 1px solid var(--border);
  color: var(--fg-muted);
  font-size: 11.5px;
  padding: 10px 16px;
  margin-top: 24px;
}
.repo-table { border-collapse: collapse; width: 100%; }
.repo-table th {
  text-align: left;
  font-size: 12px;
  color: var(--fg-muted);
  border-bottom: 1px solid var(--border);
  padding: 6px 10px;
}
.repo-table td { padding: 6px 10px; border-bottom: 1px solid var(--border); font-size: 13px; }
.print-code {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 10px 0;
  overflow: auto;
}
.print-code pre { padding: 0 12px; }
</style>
</head>
  `,

	// External JS dependencies were removed with the Bootstrap rewrite;
	// kept as an (empty) template so page templates can keep referencing it.
	"jsdep": `
<script>
function csToggleTheme() {
  var root = document.documentElement;
  var dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  var cur = root.getAttribute("data-theme") || (dark ? "dark" : "light");
  var next = cur === "dark" ? "light" : "dark";
  root.setAttribute("data-theme", next);
  localStorage.setItem("cs-theme", next);
}
</script>
`,

	// the template for the search box.
	"searchbox": `
<form action="search">
  <input placeholder="Search code across the org…" autofocus
          {{if .Query}}
          value={{.Query}}
          {{end}}
          id="searchbox" type="text" name="q">
  <button>Search</button>
</form>
`,

	"navbar": `
<div class="topbar">
  <a class="brand" href="/">codesearch</a>
  <form action="search" style="display: contents">
    <input placeholder="Search code across the org…" role="search"
          id="navsearchbox" type="text" name="q" autofocus
          {{if .Query}}
          value={{.Query}}
          {{end}}>
    <span class="numfield">Results <input type="number" id="maxhits" name="num" value="{{.Num}}"></span>
    <span class="numfield">Context <input type="number" id="context" name="ctx" value="{{.Ctx}}"></span>
    <button>Search</button>
    <!--Hack: we use a hidden form field to keep track of the debug flag across searches-->
    {{if .Debug}}<input id="debug" name="debug" type="hidden" value="{{.Debug}}">{{end}}
  </form>
  <button type="button" class="btn-ghost" onclick="csToggleTheme()" title="toggle light/dark">◐</button>
</div>
<script>
document.onkeydown=function(e){
  var e = e || window.event;
  if (e.key == "/") {
    var navbox = document.getElementById("navsearchbox");
    if (document.activeElement !== navbox) {
      navbox.focus();
      return false;
    }
  }
};
</script>
`,
	// search box for the entry page.
	"search": `
<html>
{{template "head"}}
<title>codesearch</title>
<body>
  <div class="hero">
    <h1>codesearch</h1>
    {{template "searchbox" .Last}}
  </div>

  <div class="container">
    <div class="help-cols">
      <div>
        <h3>Search examples</h3>
        <dl class="examples">
          <dt><a href="search?q=needle">needle</a></dt><dd>search for "needle"</dd>
          <dt><a href="search?q=thread+or+needle">thread or needle</a></dt><dd>either "thread" or "needle"</dd>
          <dt><a href="search?q=class+Needle">class Needle</a></dt><dd>"class" (any case) and "Needle" (case sensitive)</dd>
          <dt><a href="search?q=class+Needle+case:yes">class Needle case:yes</a></dt><dd>both, case sensitively</dd>
          <dt><a href="search?q=%22class Needle%22">"class Needle"</a></dt><dd>the exact phrase</dd>
          <dt><a href="search?q=needle+-hay">needle -hay</a></dt><dd>"needle" but not "hay"</dd>
          <dt><a href="search?q=path+file:java">path file:java</a></dt><dd>"path" in file names containing "java"</dd>
          <dt><a href="search?q=needle+lang%3Apython&num=50">needle lang:python</a></dt><dd>"needle" in Python code</dd>
          <dt><a href="search?q=f:%5C.c%24">f:\.c$</a></dt><dd>file names ending in ".c"</dd>
          <dt><a href="search?q=foo.*bar">foo.*bar</a></dt><dd>regular expression</dd>
          <dt><a href="search?q=sym:data">sym:data</a></dt><dd>symbol definitions containing "data"</dd>
          <dt><a href="search?q=phone+r:droid">phone r:droid</a></dt><dd>"phone" in repos whose name contains "droid"</dd>
          <dt><a href="search?q=phone+archived:no">phone archived:no</a></dt><dd>skip archived repos</dd>
          <dt><a href="search?q=phone+b:HEAD">phone b:HEAD</a></dt><dd>only the default branch</dd>
        </dl>
      </div>
      <div>
        <h3>List repositories</h3>
        <dl class="examples">
          <dt><a href="search?q=r:droid">r:droid</a></dt><dd>repos whose name contains "droid"</dd>
          <dt><a href="search?q=r:go+-r:google">r:go -r:google</a></dt><dd>name contains "go" but not "google"</dd>
        </dl>
      </div>
    </div>
  </div>
  <div class="footer">
    {{template "footerBoilerplate"}}
    · Used {{HumanUnit .Stats.IndexBytes}} mem for
    {{.Stats.Documents}} documents ({{HumanUnit .Stats.ContentBytes}})
    from {{.Stats.Repos}} repositories.
  </div>
  {{ template "jsdep"}}
  <script>
  var box = document.getElementById("searchbox");
  if (box) { box.focus(); }
  </script>
</body>
</html>
`,
	"footerBoilerplate": `<a href="about">About</a>`,
	"results": `
<html>
{{template "head"}}
<title>Results for {{.QueryStr}}</title>
<script>
  function zoektAddQ(atom) {
      window.location.href = "/search?q=" + encodeURIComponent("{{.QueryStr}}" + " " + atom) +
	  "&" + "num=" + {{.Last.Num}};
  }
</script>
<body id="results">
  {{template "navbar" .Last}}
  <div class="results-wrap">
    <div class="result-stats">
      {{if .Stats.Crashes}}<b>{{.Stats.Crashes}} shards crashed</b> · {{end}}
      {{ $fileCount := len .FileMatches }}
      Found <b>{{.Stats.MatchCount}}</b> results in <b>{{.Stats.FileCount}}</b> files{{if or (lt $fileCount .Stats.FileCount) (or (gt .Stats.ShardsSkipped 0) (gt .Stats.FilesSkipped 0)) }},
        showing top {{ $fileCount }} files (<a rel="nofollow"
           href="search?q={{.Last.Query}}&num={{More .Last.Num}}">show more</a>).
      {{else}}.{{end}}
    </div>
    {{range .FileMatches}}
    <div class="file-card">
      <div class="file-card-header">
        {{if .URL}}<a name="{{.ResultID}}" class="result"></a><a href="{{.URL}}" >{{else}}<a name="{{.ResultID}}">{{end}}{{.Repo}}:{{.FileName}}{{if .ScoreDebug}} <i>({{.ScoreDebug}})</i>{{end}}</a>
        {{if .Branches}}{{range .Branches}}<span class="chip">{{.}}</span> {{end}}{{end}}
        {{if .Language}}<button type="button"
             title="restrict search to files written in {{.Language}}"
             onclick="zoektAddQ('lang:&quot;{{.Language}}&quot;')" class="chip">{{.Language}}</button>{{end}}
        {{if .DuplicateID}}<a class="chip" href="#{{.DuplicateID}}">Duplicate result</a>{{end}}
      </div>
      {{if not .DuplicateID}}
      <div class="snippets">
      <table>
      <tbody>
        {{range .Matches}}
        {{if gt .LineNum 0}}
        <tr>
          <td class="lnums">
<pre>{{$beforeLines := AddLineNumbers .Before .LineNum true}}{{range $line := $beforeLines}}<span class="noselect"><u>{{$line.LineNum}}</u></span>
{{end}}<span class="noselect">{{if .URL}}<a href="{{.URL}}">{{end}}<u>{{.LineNum}}</u>{{if .URL}}</a>{{end}}</span>
{{$afterLines := AddLineNumbers .After .LineNum false}}{{range $line := $afterLines}}<span class="noselect"><u>{{$line.LineNum}}</u></span>
{{end}}</pre>
          </td>
          <td>
<pre><p style="margin: 0px;">{{range $line := $beforeLines}} {{$line.Content}}
{{end}}</p> {{range .Fragments}}{{LimitPre 100 .Pre}}<b>{{.Match}}</b>{{LimitPost 100 (TrimTrailingNewline .Post)}}{{end}}<p style="margin: 0px;">{{range $line := $afterLines}} {{$line.Content}}
{{end}}</p>{{if .ScoreDebug}}<i>({{.ScoreDebug}})</i>{{end}}</pre>
          </td>
        </tr>
        {{end}}
        {{end}}
      </tbody>
      </table>
      </div>
      {{end}}
    </div>
    {{end}}
  </div>

  <div class="footer">
    {{template "footerBoilerplate"}}
    · Took {{.Stats.Duration}}{{if .Stats.Wait}} (queued: {{.Stats.Wait}}){{end}} for
    {{HumanUnit .Stats.IndexBytesLoaded}}B index data,
    {{.Stats.NgramMatches}} ngram matches,
    {{.Stats.FilesConsidered}} docs considered,
    {{.Stats.FilesLoaded}} docs ({{HumanUnit .Stats.ContentBytesLoaded}}B) loaded,
    {{.Stats.ShardsScanned}} shards scanned,
    {{.Stats.ShardsSkippedFilter}} shards filtered
    {{- if or .Stats.FilesSkipped .Stats.ShardsSkipped -}}
      , {{.Stats.FilesSkipped}} docs skipped, {{.Stats.ShardsSkipped}} shards skipped
    {{- end -}}
	.
  </div>
  {{ template "jsdep"}}
</body>
</html>
`,

	"repolist": `
<html>
{{template "head"}}
<body id="results">
  {{template "navbar" .Last}}
  <div class="results-wrap">
    <div class="result-stats">
    Found {{.Stats.Repos}} repositories ({{.Stats.Documents}} files, {{HumanUnit .Stats.ContentBytes}}B content)
    </div>
    <table class="repo-table">
      <thead>
	<tr>
	  {{- define "q"}}q={{.Last.Query}}{{if (gt .Last.Num 0)}}&num={{.Last.Num}}{{end}}{{end}}
	  <th>Name <a href="/search?{{template "q" .}}&order=name">▼</a><a href="/search?{{template "q" .}}&order=revname">▲</a></th>
	  <th>Last updated <a href="/search?{{template "q" .}}&order=revtime">▼</a><a href="/search?{{template "q" .}}&order=time">▲</a></th>
	  <th>Branches</th>
	  <th>Size <a href="/search?{{template "q" .}}&order=revsize">▼</a><a href="/search?{{template "q" .}}&order=size">▲</a></th>
	  <th>RAM <a href="/search?{{template "q" .}}&order=revram">▼</a><a href="/search?{{template "q" .}}&order=ram">▲</a></th>
	</tr>
      </thead>
      <tbody>
	{{range .Repos -}}
	<tr>
	  <td>{{if .URL}}<a href="{{.URL}}">{{end}}{{.Name}}{{if .URL}}</a>{{end}}</td>
	  <td><small>{{.IndexTime.Format "Jan 02, 2006 15:04"}}</small></td>
	  <td>
	    {{- range .Branches -}}
	    {{if .URL}}<a class="chip" href="{{.URL}}">{{.Name}}</a>{{else}}<span class="chip">{{.Name}}</span>{{end}}&nbsp;
	    {{- end -}}
	  </td>
	  <td><small>{{HumanUnit .Files}} files ({{HumanUnit .Size}}B)</small></td>
	  <td><small>{{HumanUnit .MemorySize}}B</td>
	</tr>
	{{end}}
      </tbody>
    </table>
  </div>

  <div class="footer">
    {{template "footerBoilerplate"}}
  </div>

  {{ template "jsdep"}}
</body>
</html>
`,

	"print": `
<html>
  {{template "head"}}
  <title>{{.Repo}}:{{.Name}}</title>
<body id="results">
  {{template "navbar" .Last}}
  <div class="results-wrap">
     <div class="result-stats"><b>{{.Repo}}</b>:{{.Name}}</div>
     <div class="print-code">
       {{ range $index, $ln := .Lines}}
	 <pre id="l{{Inc $index}}"><span class="noselect"><a href="#l{{Inc $index}}">{{Inc $index}}</a> </span>{{$ln}}</pre>
       {{end}}
     </div>
  <div class="footer">
    {{template "footerBoilerplate"}}
  </div>
  </div>
 {{ template "jsdep"}}
</body>
</html>
`,

	"about": `

<html>
  {{template "head"}}
  <title>About codesearch</title>
<body>

  <div class="hero">
    <h1>codesearch</h1>
    {{template "searchbox" .Last}}
  </div>

  <div class="container">
    <p>
      This is the internal code search service, powered by
      <a href="http://github.com/sourcegraph/zoekt"><em>zoekt</em></a>,
      an open-source full text search engine.
    </p>
    <p>
    {{if .Version}}<em>Zoekt</em> version {{.Version}}, uptime{{else}}Uptime{{end}} {{.Uptime}}
    </p>

    <p>
    Used {{HumanUnit .Stats.IndexBytes}} memory for
    {{.Stats.Documents}} documents ({{HumanUnit .Stats.ContentBytes}})
    from {{.Stats.Repos}} repositories.
    </p>
  </div>

  <div class="footer">
    {{template "footerBoilerplate"}}
  </div>
  {{ template "jsdep"}}
`,
	"robots": `
user-agent: *
disallow: /search
`,
}

func init() {
	for k, v := range TemplateText {
		_, err := Top.New(k).Parse(v)
		if err != nil {
			log.Panicf("parse(%s): %v:", k, err)
		}
	}
}
