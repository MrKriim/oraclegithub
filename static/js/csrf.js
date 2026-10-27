(function(){
  function getCookie(name){
    var m = document.cookie.match('(^|;)\\s*' + name + '\\s*=\\s*([^;]+)');
    return m ? decodeURIComponent(m.pop()) : '';
  }
  function csrfToken(){
    return getCookie('csrf');
  }

  document.addEventListener('htmx:configRequest', function(evt){
    var method = (evt.detail.verb || '').toUpperCase();
    if(method === 'GET' || method === 'HEAD' || method === 'OPTIONS') return;
    var token = csrfToken();
    if(token) evt.detail.headers['X-CSRF-Token'] = token;
  });

  var originalFetch = window.fetch;
  window.fetch = function(input, init){
    init = init || {};
    var method = (init.method || 'GET').toUpperCase();
    if(method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS'){
      var url = typeof input === 'string' ? input : (input && input.url) || '';
      var sameOrigin = !url || url.indexOf('http') !== 0 || url.indexOf(window.location.origin) === 0;
      if(sameOrigin){
        var token = csrfToken();
        if(token){
          if(init.headers instanceof Headers){
            init.headers.set('X-CSRF-Token', token);
          } else {
            init.headers = init.headers || {};
            init.headers['X-CSRF-Token'] = token;
          }
        }
      }
    }
    return originalFetch(input, init);
  };
})();
