// Дерево файлов ревью: по папкам и алфавиту, папки сначала. Общая для всех файлов часть пути не
// повторяется; цепочка папок, в каждой из которых только одна папка, — одной строкой («server/web/»).

export function commonDir(paths) {
  if (!paths.length) return '';
  const dirs = paths.map((p) => p.split('/').slice(0, -1));
  let n = dirs[0].length;
  for (const d of dirs) {
    let k = 0;
    while (k < n && k < d.length && d[k] === dirs[0][k]) k++;
    n = k;
  }
  return dirs[0].slice(0, n).join('/');
}

// buildTree — дерево: { dirs: [{name, key, node}], files: [FileView] }.
export function buildTree(files) {
  const root = commonDir(files.map((f) => f.path));
  const top = { dirs: new Map(), files: [] };
  for (const f of files) {
    const rel = root ? f.path.slice(root.length + 1) : f.path.replace(/^\//, '');
    const parts = rel.split('/');
    let node = top;
    for (const p of parts.slice(0, -1)) {
      if (!node.dirs.has(p)) node.dirs.set(p, { dirs: new Map(), files: [] });
      node = node.dirs.get(p);
    }
    node.files.push({ ...f, name: parts[parts.length - 1] });
  }
  const cmp = (a, b) => a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true });
  const fix = (node, prefix) => {
    const dirs = [...node.dirs.entries()].sort((a, b) => cmp(a[0], b[0])).map(([name, n]) => {
      // Сжимаем цепочку папок без файлов.
      let nm = name;
      let cur = n;
      while (cur.files.length === 0 && cur.dirs.size === 1) {
        const [[k, v]] = cur.dirs.entries();
        nm += '/' + k;
        cur = v;
      }
      const key = prefix + nm + '/';
      return { name: nm + '/', key, node: fix(cur, key) };
    });
    const fs = node.files.sort((a, b) => cmp(a.name, b.name));
    return { dirs, files: fs };
  };
  return { root, tree: fix(top, '') };
}

// flatten — строки дерева с учётом свёрнутых папок. Каждая: {dir, depth, name, key} или {file, depth}.
export function flatten(tree, collapsed = new Set()) {
  const out = [];
  const walk = (node, depth) => {
    for (const d of node.dirs) {
      out.push({ dir: true, depth, name: d.name, key: d.key, open: !collapsed.has(d.key) });
      if (!collapsed.has(d.key)) walk(d.node, depth + 1);
    }
    for (const f of node.files) out.push({ file: f, depth });
  };
  walk(tree, 0);
  return out;
}

// fileOrder — все файлы в порядке дерева (свёрнутые папки не важны).
export function fileOrder(tree) {
  return flatten(tree).filter((r) => r.file).map((r) => r.file);
}

export const isAccepted = (f) => f.state === 'accepted' || f.state === 'deleted-accepted';
export const counts = (files) => {
  const fs = files.filter((f) => f.state !== 'moved');
  return { done: fs.filter(isAccepted).length, total: fs.length };
};
