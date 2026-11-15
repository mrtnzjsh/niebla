/**
 * 🌴 Niebla Docs - Collapsible Sidebar & Table-of-Contents Controller
 * 
 * Provides:
 * 1. Persistent toggle controls for Left Nav Sidebar and Right Table of Contents.
 * 2. Keyboard shortcuts: `[` toggles Left Nav, `]` toggles Right TOC.
 * 3. Edge-floating restore tabs when sidebars are collapsed.
 * 4. Zero-FOUC state synchronization via localStorage.
 */

(function () {
  'use strict';

  const STORAGE_KEY_NAV = 'niebla-nav-collapsed';
  const STORAGE_KEY_TOC = 'niebla-toc-collapsed';

  function isNavCollapsed() {
    return document.documentElement.dataset.navCollapsed === 'true';
  }

  function isTocCollapsed() {
    return document.documentElement.dataset.tocCollapsed === 'true';
  }

  function setNavCollapsed(collapsed) {
    if (collapsed) {
      document.documentElement.dataset.navCollapsed = 'true';
      localStorage.setItem(STORAGE_KEY_NAV, 'true');
    } else {
      delete document.documentElement.dataset.navCollapsed;
      localStorage.setItem(STORAGE_KEY_NAV, 'false');
    }
    updateButtonStates();
  }

  function setTocCollapsed(collapsed) {
    if (collapsed) {
      document.documentElement.dataset.tocCollapsed = 'true';
      localStorage.setItem(STORAGE_KEY_TOC, 'true');
    } else {
      delete document.documentElement.dataset.tocCollapsed;
      localStorage.setItem(STORAGE_KEY_TOC, 'false');
    }
    updateButtonStates();
  }

  function toggleNav() {
    setNavCollapsed(!isNavCollapsed());
  }

  function toggleToc() {
    setTocCollapsed(!isTocCollapsed());
  }

  function updateButtonStates() {
    const navBtn = document.getElementById('niebla-toggle-nav');
    if (navBtn) {
      const collapsed = isNavCollapsed();
      navBtn.setAttribute('aria-expanded', String(!collapsed));
      navBtn.title = collapsed ? 'Expand Navigation Sidebar ([)' : 'Collapse Navigation Sidebar ([)';
      navBtn.setAttribute('data-active', String(!collapsed));
    }

    const tocBtn = document.getElementById('niebla-toggle-toc');
    if (tocBtn) {
      const collapsed = isTocCollapsed();
      tocBtn.setAttribute('aria-expanded', String(!collapsed));
      tocBtn.title = collapsed ? 'Expand Table of Contents (])' : 'Collapse Table of Contents (])';
      tocBtn.setAttribute('data-active', String(!collapsed));
    }

    const edgeNav = document.getElementById('niebla-edge-nav');
    if (edgeNav) {
      edgeNav.style.display = isNavCollapsed() ? 'flex' : 'none';
    }

    const edgeToc = document.getElementById('niebla-edge-toc');
    if (edgeToc) {
      const hasToc = document.documentElement.hasAttribute('data-has-toc');
      edgeToc.style.display = (hasToc && isTocCollapsed()) ? 'flex' : 'none';
    }
  }

  function injectNavToggle() {
    if (document.getElementById('niebla-toggle-nav')) return;
    const titleWrapper = document.querySelector('.title-wrapper');
    if (!titleWrapper) return;

    const btn = document.createElement('button');
    btn.id = 'niebla-toggle-nav';
    btn.type = 'button';
    btn.className = 'niebla-toggle-btn niebla-nav-btn';
    btn.innerHTML = `
      <svg class="niebla-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="3" width="18" height="18" rx="2" ry="2"></rect>
        <line x1="9" y1="3" x2="9" y2="21"></line>
        <path class="niebla-arrow-nav" d="m14 9-3 3 3 3"></path>
      </svg>
      <span class="niebla-kbd-hint">[</span>
    `;
    btn.addEventListener('click', (e) => {
      e.preventDefault();
      toggleNav();
    });

    titleWrapper.appendChild(btn);
  }

  function injectTocToggle() {
    if (document.getElementById('niebla-toggle-toc')) return;
    const hasToc = document.documentElement.hasAttribute('data-has-toc');
    if (!hasToc) return;

    const rightGroup = document.querySelector('.header .right-group');
    if (!rightGroup) return;

    const btn = document.createElement('button');
    btn.id = 'niebla-toggle-toc';
    btn.type = 'button';
    btn.className = 'niebla-toggle-btn niebla-toc-btn';
    btn.innerHTML = `
      <span class="niebla-toggle-text">TOC</span>
      <svg class="niebla-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="3" width="18" height="18" rx="2" ry="2"></rect>
        <line x1="15" y1="3" x2="15" y2="21"></line>
        <path class="niebla-arrow-toc" d="m10 9 3 3-3 3"></path>
      </svg>
      <span class="niebla-kbd-hint">]</span>
    `;
    btn.addEventListener('click', (e) => {
      e.preventDefault();
      toggleToc();
    });

    // Insert as the first child of rightGroup (before social icons)
    rightGroup.insertBefore(btn, rightGroup.firstChild);
  }

  function injectEdgeTabs() {
    if (!document.getElementById('niebla-edge-nav')) {
      const edgeNav = document.createElement('button');
      edgeNav.id = 'niebla-edge-nav';
      edgeNav.type = 'button';
      edgeNav.className = 'niebla-edge-pill niebla-edge-left';
      edgeNav.title = 'Expand Navigation Sidebar ([)';
      edgeNav.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="9 18 15 12 9 6"></polyline>
        </svg>
        <span>Nav</span>
      `;
      edgeNav.addEventListener('click', () => setNavCollapsed(false));
      document.body.appendChild(edgeNav);
    }

    if (!document.getElementById('niebla-edge-toc')) {
      const edgeToc = document.createElement('button');
      edgeToc.id = 'niebla-edge-toc';
      edgeToc.type = 'button';
      edgeToc.className = 'niebla-edge-pill niebla-edge-right';
      edgeToc.title = 'Expand Table of Contents (])';
      edgeToc.innerHTML = `
        <span>TOC</span>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="15 18 9 12 15 6"></polyline>
        </svg>
      `;
      edgeToc.addEventListener('click', () => setTocCollapsed(false));
      document.body.appendChild(edgeToc);
    }
  }

  function setupKeyboardShortcuts() {
    if (window.__nieblaKeyboardAttached) return;
    window.__nieblaKeyboardAttached = true;

    window.addEventListener('keydown', (e) => {
      const tag = e.target && e.target.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || (e.target && e.target.isContentEditable)) {
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;

      if (e.key === '[') {
        e.preventDefault();
        toggleNav();
      } else if (e.key === ']') {
        const hasToc = document.documentElement.hasAttribute('data-has-toc');
        if (hasToc) {
          e.preventDefault();
          toggleToc();
        }
      }
    });
  }

  function wrapTables() {
    const tables = document.querySelectorAll('.sl-markdown-content > table');
    tables.forEach((table) => {
      if (table.parentElement && table.parentElement.classList.contains('niebla-table-wrap')) {
        return;
      }
      const wrapper = document.createElement('div');
      wrapper.className = 'niebla-table-wrap';
      table.parentNode.insertBefore(wrapper, table);
      wrapper.appendChild(table);
    });
  }

  function init() {
    injectNavToggle();
    injectTocToggle();
    injectEdgeTabs();
    setupKeyboardShortcuts();
    updateButtonStates();
    wrapTables();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // Support Astro view transitions or SPA navigation
  document.addEventListener('astro:page-load', init);
})();
