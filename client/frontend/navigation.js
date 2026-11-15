// navigation.js 仅管理工作台导航和移动侧栏；业务数据与任务独立于当前页面。
import {viewForHash} from './view.mjs';

const titles = {
  overview:['信息总览','连接公开历史，整理自己的关注。'],
  creators:['UP 主库','管理关注的创作者，发现服务器已记录的 UP。'],
  videos:['视频库','整理个人关注的视频，查看播放变化与历史版本。'],
  discovery:['投稿发现','按需获取可见投稿，选择你希望持续跟踪的视频。'],
  settings:['设置','管理连接、工作目录与本地登录状态。']
};
export function initNavigation(onChange) {
  const sidebar = document.getElementById('sidebar');
  const toggle = document.getElementById('menu-toggle');
  const backdrop = document.getElementById('sidebar-backdrop');
  const mobile = matchMedia('(max-width:700px)');
  function menu(open) {
    sidebar.classList.toggle('open',open);backdrop.hidden = !open;
    toggle.setAttribute('aria-expanded',String(open));sidebar.inert = mobile.matches && !open;
  }
  function update() {
    const view = viewForHash(location.hash);
    document.querySelectorAll('[data-page]').forEach(el => {el.hidden = el.dataset.page !== (view === 'creators' || view === 'videos' ? 'library' : view);});
    document.querySelectorAll('[data-view]').forEach(el => {if(el.dataset.view === view)el.setAttribute('aria-current','page');else el.removeAttribute('aria-current');});
    document.getElementById('page-title').textContent = titles[view][0];
    document.getElementById('page-subtitle').textContent = titles[view][1];
    document.getElementById('breadcrumb-current').textContent = titles[view][0];
    document.title = `${titles[view][0]} · Bili History Tracker`;
    menu(false);window.scrollTo(0,0);onChange(view);
  }
  function navigate(view) {
    const hash = `#${viewForHash(`#${view}`)}`;
    if(location.hash === hash)update();else location.hash = hash;
  }
  document.querySelectorAll('[data-view]').forEach(el => el.addEventListener('click',event => {
    if(event.ctrlKey||event.metaKey||event.shiftKey||event.altKey)return;
    event.preventDefault();navigate(el.dataset.view);
  }));
  document.querySelectorAll('[data-goto]').forEach(el => el.addEventListener('click',() => navigate(el.dataset.goto)));
  toggle.addEventListener('click',() => menu(toggle.getAttribute('aria-expanded') !== 'true'));
  backdrop.addEventListener('click',() => menu(false));
  document.addEventListener('keydown',event => {if(event.key === 'Escape')menu(false);});
  mobile.addEventListener('change',() => menu(false));
  window.addEventListener('hashchange',update);menu(false);
  return {navigate,update,current:() => viewForHash(location.hash)};
}
