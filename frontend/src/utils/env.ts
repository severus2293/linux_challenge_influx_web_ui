import { execSync } from 'child_process'

// Получаем GIT_SHA из переменной окружения или Git, с fallback на "unknown"
let GIT_SHA: string;
try {
  GIT_SHA =
    process.env.UI_SHA ||
    execSync('git rev-parse --sq HEAD').toString().trim();
} catch (error) {
  GIT_SHA = 'unknown';
}

// Форматирование STATIC_DIRECTORY для Webpack
function formatStatic(dir: string): string {
  if (!dir.length) {
    return dir;
  }

  let _dir = dir.slice(0);
  if (_dir[0] === '/') {
    _dir = _dir.slice(1);
  }
  if (_dir[_dir.length - 1] !== '/') {
    _dir = _dir + '/';
  }

  return _dir;
}

// Форматирование BASE_PATH и API_BASE_PATH для Webpack
function formatBase(prefix: string): string {
  if (prefix === '/') {
    return prefix;
  }

  let _prefix = prefix.slice(0);
  if (_prefix[0] !== '/') {
    _prefix = '/' + _prefix;
  }
  if (_prefix[_prefix.length - 1] !== '/') {
    _prefix = _prefix + '/';
  }

  return _prefix;
}

const STATIC_DIRECTORY = formatStatic(process.env.STATIC_DIRECTORY || '');
const BASE_PATH = formatBase(process.env.BASE_PATH || '/');
const API_BASE_PATH = formatBase(process.env.API_BASE_PATH || '/');

export {
  formatStatic,
  formatBase,
  GIT_SHA,
  STATIC_DIRECTORY,
  BASE_PATH,
  API_BASE_PATH,
};
