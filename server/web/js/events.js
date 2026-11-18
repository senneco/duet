// Все вкладки одного браузера получают события через одно HTTP-соединение.
export function subscribeEvents(handlers) {
  let worker;
  let stream;

  // В браузере без общего worker подписана видимая вкладка. При возврате
  // сервер присылает hello, и клиент перечитывает актуальное ревью.
  function visibility() {
    if (document.hidden) {
      stream?.close();
      stream = null;
    } else if (!stream) {
      stream = new EventSource('/api/events');
      for (const [name, handler] of Object.entries(handlers)) stream.addEventListener(name, handler);
    }
  }

  function fallback() {
    worker?.port.close();
    document.addEventListener('visibilitychange', visibility);
    visibility();
  }

  try {
    worker = new SharedWorker('/web/js/event-worker.js');
    worker.port.onmessage = ({ data }) => handlers[data.type]?.(data);
    worker.onerror = fallback;
  } catch {
    fallback();
  }

  return () => {
    if (worker) {
      worker.onerror = null;
      worker.port.postMessage('leave');
      worker.port.close();
    }
    document.removeEventListener('visibilitychange', visibility);
    stream?.close();
  };
}
