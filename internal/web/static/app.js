(() => {
  'use strict';

  const menu = document.querySelector('.menu-toggle');
  const navigation = document.getElementById('mobile-navigation');
  const closeMenu = () => {
    document.body.classList.remove('nav-open');
    if (menu) {
      menu.setAttribute('aria-expanded', 'false');
      menu.setAttribute('aria-label', 'Open navigation');
    }
  };
  if (menu && navigation) {
    menu.addEventListener('click', () => {
      const open = document.body.classList.toggle('nav-open');
      menu.setAttribute('aria-expanded', String(open));
      menu.setAttribute('aria-label', open ? 'Close navigation' : 'Open navigation');
    });
    navigation.addEventListener('click', (event) => {
      if (event.target.closest('a')) closeMenu();
    });
    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && document.body.classList.contains('nav-open')) {
        closeMenu();
        menu.focus();
      }
    });
    document.addEventListener('click', (event) => {
      if (!navigation.contains(event.target) && !menu.contains(event.target)) closeMenu();
    });
    window.matchMedia('(min-width: 761px)').addEventListener('change', closeMenu);
  }

  const enhancePasswords = (root) => {
    root.querySelectorAll('input[type="password"]').forEach((input) => {
      if (input.closest('.password-wrap')) return;
      const wrapper = document.createElement('span');
      wrapper.className = 'password-wrap';
      const toggle = document.createElement('button');
      toggle.className = 'password-toggle';
      toggle.type = 'button';
      toggle.textContent = 'Show';
      const label = input.closest('label');
      const text = label && label.firstChild ? label.firstChild.textContent.trim() : '';
      const name = (text || input.name.replaceAll('_', ' ')).toLowerCase();
      toggle.setAttribute('aria-label', 'Show ' + name);
      toggle.setAttribute('aria-pressed', 'false');
      input.parentNode.insertBefore(wrapper, input);
      wrapper.append(input, toggle);
      toggle.addEventListener('click', () => {
        const reveal = input.type === 'password';
        input.type = reveal ? 'text' : 'password';
        toggle.textContent = reveal ? 'Hide' : 'Show';
        toggle.setAttribute('aria-pressed', String(reveal));
        toggle.setAttribute('aria-label', (reveal ? 'Hide ' : 'Show ') + name);
      });
    });
  };
  enhancePasswords(document);

  document.querySelectorAll('[data-search]').forEach((search) => {
    const table = document.querySelector(search.dataset.search);
    if (!table) return;
    const rows = [...table.querySelectorAll('[data-search-row]')];
    const card = table.closest('.card');
    const empty = card ? card.querySelector('.search-empty') : null;
    search.addEventListener('input', () => {
      const query = search.value.trim().toLocaleLowerCase();
      let visible = 0;
      rows.forEach((row) => {
        row.hidden = !row.textContent.toLocaleLowerCase().includes(query);
        if (!row.hidden) visible += 1;
      });
      if (empty) empty.hidden = visible !== 0;
    });
  });

  document.querySelectorAll('form').forEach((form) => {
    const password = form.querySelector('input[name="password"]');
    const confirmation = form.querySelector('input[name="again"]');
    if (!password || !confirmation) return;
    const validate = () => confirmation.setCustomValidity(
      confirmation.value && password.value !== confirmation.value ? 'These passwords differ. Enter the same password in both fields.' : ''
    );
    password.addEventListener('input', validate);
    confirmation.addEventListener('input', validate);
  });

  let noticeTimer;
  const notice = document.getElementById('request-notice');
  const showRequestError = (message) => {
    if (!notice) return;
    notice.textContent = message;
    notice.hidden = false;
    clearTimeout(noticeTimer);
    noticeTimer = setTimeout(() => { notice.hidden = true; }, 8000);
  };
  document.addEventListener('htmx:afterSwap', (event) => enhancePasswords(event.detail.target));
  document.addEventListener('htmx:sendError', () => showRequestError('We could not connect. Check your connection and try again.'));
  document.addEventListener('htmx:responseError', (event) => {
    if (event.detail.xhr.status !== 401) showRequestError('That request could not be completed. Please try again.');
  });
})();
