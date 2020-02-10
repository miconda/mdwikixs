{{define "header"}}
<!doctype html>
<head>
 <meta charset="UTF-8">
 <title>MWikiXS</title>
 <link href="/assets/css/bootstrap.min.css" rel="stylesheet">
 <meta name="viewport" content="width=device-width, initial-scale=1">
</head>
<body>
<div class="container">
 <div class="row col-md-9">
   <ol class="breadcrumb">
    <li class="active"><a href="/index">(home)</a></li>
    {{range $dir := .Dirs }}
     {{if $dir.Active }}
      <li class="active"><a href="{{ $dir.Path }}">{{$dir.Name}}</a></li>
     {{ else }}
      <li><a href="{{ $dir.Path }}">{{$dir.Name}}</a></li>
     {{ end }}
    {{ end }}
   </ol>
 </div>
{{end}}
