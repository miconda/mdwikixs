{{ define "revision" }}<
 <!-- Actions for a specific revision (revert, diff etc) -->
<div class="row col-md-9">
 <form method="POST">
  <div class="form-group">
   <button type="submit" class="btn btn-danger btn-xs">
    <span class="glyphicon glyphicon-step-backward"></span> Revert
   </button>
   <input type="hidden" name="revert" value="{{ .Revision }}" />
  </div>
 </form>
</div>
{{end}}
