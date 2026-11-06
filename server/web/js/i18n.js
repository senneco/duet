// Тексты интерфейса на русском и английском. Язык по умолчанию — язык системы.

const plRu = (n, one, few, many) => {
  const a = n % 10, b = n % 100;
  return a === 1 && b !== 11 ? one : a >= 2 && a <= 4 && (b < 12 || b > 14) ? few : many;
};

const RU = {
  reviews: 'Ревью', newestFirst: 'новые сверху', colTitle: 'название', colCreated: 'создано', colAccepted: 'принято', colAgent: 'агент',
  noReviews: 'Ревью пока нет. Попросите агента отдать работу на ревью — он пришлёт ссылку.',
  noReview: 'Такого ревью нет.', noFiles: 'В ревью нет файлов.',
  unviewed: 'непросмотренный ответ', acceptedAll: 'принято целиком',
  agentNone: 'не подключён', agentWaits: 'ждёт', agentBusy: 'занят', agentWorks: 'работает', agentStopping: 'останавливается…', agentLost: 'пропал посреди работы',
  modeRead: 'ЧТЕНИЕ', modeList: 'СПИСОК', modeMenu: 'МЕНЮ', modeHelp: 'СПРАВКА', modeOff: 'НЕТ СВЯЗИ',
  stNew: 'не открывали', stOpened: 'открывали', stAccepted: 'принят', stDeleted: 'удалён', stMoved: 'перешёл в другое ревью',
  stWorking: 'агент работает по комментариям', stRipple: 'непросмотренный ответ', stDisk: 'изменён на диске', stDiskShort: 'изм. на диске', stNewFile: 'новый файл',
  doc: 'документ', source: 'исходник', one: 'одна колонка', two: 'две колонки',
  themeSystem: 'как macOS', themeDark: 'тёмная', themeLight: 'светлая',
  accept: 'принять', accepted: 'принят',
  base: 'база', beforeTask: 'до задачи', current: 'текущая', onDisk: 'на диске', noBase: 'новый файл — базы нет',
  was: 'было', now: 'стало', added: 'новое', removed: 'удалено', more: 'дописано', changed: 'изменено',
  blankAdded: 'добавлена пустая строка', blankRemoved: 'удалена пустая строка',
  fold: (n) => `ещё ${n} ${plRu(n, 'строка', 'строки', 'строк')}`,
  syncing: 'сверяю с диском',
  fileDeleted: 'Файл удалён', fileNew: 'Новый файл', movedTo: (t) => `Файл перешёл в ревью «${t}»`,
  menuTitle: 'действия', menuFile: 'файл', menuAccept: 'принять файл', menuAcceptNext: 'и открыть следующий', menuLater: 'появится позже',
  menuComment: 'комментарий', menuSend: 'отправить агенту', menuResponse: 'ответ агента', menuEdit: 'править', close: 'закрыть',
  menu: 'меню', keys: 'клавиши', allKeys: 'Клавиши', reading: 'чтение', navigation: 'переход', view: 'вид', actions: 'действия',
  kScroll: 'прокрутка', kTopBottom: 'в начало / в конец файла', kChange: 'следующее / предыдущее изменение', kFile: 'следующий / предыдущий непринятый файл',
  kBlock: 'следующий / предыдущий блок агента', kMenu: 'меню действий', kAccept: 'принять файл и открыть следующий', kCols: 'одна / две колонки',
  kDoc: 'документ / исходник', kHelp: 'этот список', kEsc: 'закрыть меню и сообщения', kList: 'выбрать ревью / открыть',
  offTitle: 'duet не запущен', offText: 'Ядро остановлено. Вкладка подключится сама, как только duet запустится снова — по любой команде duet.', offWait: 'жду ядро…',
  denied: 'Принять нельзя', errUnviewed: 'Есть непросмотренный ответ агента к этому файлу. Сначала его нужно просмотреть.',
  errDisk: 'Файл изменён на диске в обход duet.',
  errMoved: 'Файл перешёл в другое ревью.', errOther: 'Не получилось', errNet: 'duet не отвечает',
  allAccepted: 'Ревью принято целиком', filesAccepted: 'Все файлы приняты',
  leftUnsent: (n) => `${n} ${plRu(n, 'комментарий не отправлен', 'комментария не отправлены', 'комментариев не отправлены')}`,
  leftQuestions: (n) => `агент спрашивал, вы не ответили: ${n}`, leftResponse: 'есть непросмотренный ответ агента',
  leftDisk: (n) => `изменены на диске: ${n}`, leftWorking: 'агент ещё работает',
  chg: 'изм', line: 'стр', today: 'сегодня', yesterday: 'вчера', back: 'все ревью',
};

const EN = {
  reviews: 'Reviews', newestFirst: 'newest first', colTitle: 'title', colCreated: 'created', colAccepted: 'accepted', colAgent: 'agent',
  noReviews: 'No reviews yet. Ask your agent to send its work for review — it will give you a link.',
  noReview: 'No such review.', noFiles: 'No files in this review.',
  unviewed: 'unviewed response', acceptedAll: 'fully accepted',
  agentNone: 'not connected', agentWaits: 'waiting', agentBusy: 'busy', agentWorks: 'working', agentStopping: 'stopping…', agentLost: 'lost mid-work',
  modeRead: 'READ', modeList: 'LIST', modeMenu: 'MENU', modeHelp: 'HELP', modeOff: 'OFFLINE',
  stNew: 'not opened', stOpened: 'opened', stAccepted: 'accepted', stDeleted: 'deleted', stMoved: 'moved to another review',
  stWorking: 'agent is working on comments', stRipple: 'unviewed response', stDisk: 'changed on disk', stDiskShort: 'changed on disk', stNewFile: 'new file',
  doc: 'document', source: 'source', one: 'one column', two: 'two columns',
  themeSystem: 'like macOS', themeDark: 'dark', themeLight: 'light',
  accept: 'accept', accepted: 'accepted',
  base: 'base', beforeTask: 'before the task', current: 'current', onDisk: 'on disk', noBase: 'new file — no base',
  was: 'was', now: 'now', added: 'new', removed: 'removed', more: 'added', changed: 'changed',
  blankAdded: 'blank line added', blankRemoved: 'blank line removed',
  fold: (n) => `${n} more ${n === 1 ? 'line' : 'lines'}`,
  syncing: 'checking disk',
  fileDeleted: 'File deleted', fileNew: 'New file', movedTo: (t) => `The file moved to review “${t}”`,
  menuTitle: 'actions', menuFile: 'file', menuAccept: 'accept file', menuAcceptNext: 'and open the next one', menuLater: 'coming later',
  menuComment: 'comment', menuSend: 'send to agent', menuResponse: 'agent response', menuEdit: 'edit', close: 'close',
  menu: 'menu', keys: 'keys', allKeys: 'Keys', reading: 'reading', navigation: 'navigation', view: 'view', actions: 'actions',
  kScroll: 'scroll', kTopBottom: 'top / bottom of the file', kChange: 'next / previous change', kFile: 'next / previous unaccepted file',
  kBlock: 'next / previous agent block', kMenu: 'actions menu', kAccept: 'accept the file and open the next', kCols: 'one / two columns',
  kDoc: 'document / source', kHelp: 'this list', kEsc: 'close menus and messages', kList: 'pick a review / open',
  offTitle: 'duet is not running', offText: 'The core has stopped. This tab reconnects by itself as soon as duet starts again — with any duet command.', offWait: 'waiting for the core…',
  denied: 'Can’t accept', errUnviewed: 'There is an unviewed agent response for this file. View it first.',
  errDisk: 'The file was changed on disk outside duet.',
  errMoved: 'The file moved to another review.', errOther: 'Something went wrong', errNet: 'duet is not responding',
  allAccepted: 'Review fully accepted', filesAccepted: 'All files accepted',
  leftUnsent: (n) => `${n} unsent ${n === 1 ? 'comment' : 'comments'}`,
  leftQuestions: (n) => `unanswered agent questions: ${n}`, leftResponse: 'an agent response is unviewed',
  leftDisk: (n) => `changed on disk: ${n}`, leftWorking: 'the agent is still working',
  chg: 'chg', line: 'ln', today: 'today', yesterday: 'yesterday', back: 'all reviews',
};

export function defaultLang() {
  const l = (navigator.languages && navigator.languages[0]) || navigator.language || 'en';
  return l.toLowerCase().startsWith('ru') ? 'ru' : 'en';
}

export const dict = (lang) => (lang === 'ru' ? RU : EN);

export function fmtDate(iso, lang) {
  const d = new Date(iso);
  const t = dict(lang);
  const now = new Date();
  const day = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(now) - day(d)) / 864e5);
  const time = d.toLocaleTimeString(lang, { hour: '2-digit', minute: '2-digit' });
  if (diff === 0) return `${t.today}, ${time}`;
  if (diff === 1) return `${t.yesterday}, ${time}`;
  const opts = { day: 'numeric', month: 'long' };
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return d.toLocaleDateString(lang, opts);
}
