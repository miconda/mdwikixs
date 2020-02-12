{{define "footer"}}
<div class="row col-md-9">
  {{if .Revision}}
    <hr class="text-muted" />
    <p class="text-muted">Revision: {{.Revision}}</p>
  {{end}}
  <hr class="text-muted" />
  <a href="https://github.com/miconda/mwikixs"><p class="text-muted text-center">MWikiXS on Github</p></a>
</div>
<!-- end container -->
</div>
 </body>
</html>
{{end}}
