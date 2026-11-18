// SharedWorker живёт, пока есть вкладки duet. Новая вкладка сразу узнаёт
// состояние потока, даже если hello пришёл до её подключения.
const ports = new Set();
let state;
const stream = new EventSource('/api/events');
for (const type of ['hello', 'error', 'change', 'reset', 'sync']) {
  stream.addEventListener(type, event => {
    const message = { type, data: event.data };
    if (type === 'hello' || type === 'error') state = message;
    for (const port of ports) port.postMessage(message);
  });
}

self.onconnect = ({ ports: [port] }) => {
  ports.add(port);
  port.onmessage = () => {
    ports.delete(port);
    port.close();
  };
  if (state) port.postMessage(state);
};
