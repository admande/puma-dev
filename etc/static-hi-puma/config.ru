# @see https://github.com/admande/puma-dev/issues/140#issuecomment-422575346
run Proc.new { |env| ['200', {'Content-Type' => 'text/plain'}, ["rack wuz here"]] }
