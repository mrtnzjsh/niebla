/**
 * 🌴 Niebla Docs - Mermaid Diagram Renderer
 * Themed with Paleta Quisqueyana (Dominican Natural Accents)
 */

(function () {
  'use strict';

  function decodeHtmlEntities(str) {
    const txt = document.createElement('textarea');
    txt.innerHTML = str;
    return txt.value;
  }

  function initMermaidTheme() {
    if (!window.mermaid) return;

    window.mermaid.initialize({
      startOnLoad: false,
      theme: 'base',
      securityLevel: 'loose',
      themeVariables: {
        darkMode: true,
        fontFamily: 'ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
        fontSize: '13px',

        // Core Surfaces (Paleta Quisqueyana Caribbean Midnight)
        background: '#080D14',
        mainBkg: '#0E1622',
        nodeBorder: '#0284C7',
        nodeTextColor: '#F1F5F9',

        // Connectors & Highlights (Larimar Blue & Azul Ultramar)
        lineColor: '#2DD4BF',
        arrowheadColor: '#2DD4BF',
        clusterBkg: '#0B1118',
        clusterBorder: '#0284C7',

        // Node Palettes
        primaryColor: '#0E1622',
        primaryTextColor: '#F1F5F9',
        primaryBorderColor: '#0284C7',

        secondaryColor: '#1A2433',
        secondaryTextColor: '#CBD5E1',
        secondaryBorderColor: '#2DD4BF',

        tertiaryColor: '#0E1622',
        tertiaryTextColor: '#CBD5E1',
        tertiaryBorderColor: '#F59E0B',

        // Sequence Specifics
        actorBkg: '#0E1622',
        actorBorder: '#0284C7',
        actorTextColor: '#F1F5F9',
        actorLineColor: '#4A5B73',
        signalColor: '#2DD4BF',
        signalTextColor: '#F1F5F9',
        labelBoxBkgColor: '#0E1622',
        labelBoxBorderColor: '#2DD4BF',
        labelTextColor: '#F1F5F9',
        loopTextColor: '#F1F5F9',
        noteBkgColor: '#1A2433',
        noteBorderColor: '#F59E0B',
        noteTextColor: '#FDE68A',

        // ER Diagram Specifics
        entityBorder: '#0284C7',
        entityBkg: '#0E1622',
        entityTextColor: '#F1F5F9',
        attributeBackgroundColorOdd: '#080D14',
        attributeBackgroundColorEven: '#0E1622',
        relationshipColor: '#2DD4BF',
        relationshipLineColor: '#2DD4BF',
        relationshipTextColor: '#99F6E4',
      },
      flowchart: {
        htmlLabels: true,
        useMaxWidth: true,
        padding: 20,
        diagramPadding: 20,
      },
      sequence: {
        diagramMarginX: 20,
        diagramMarginY: 20,
        actorMargin: 50,
        width: 150,
        height: 40,
        boxMargin: 10,
        boxTextMargin: 5,
        noteMargin: 10,
        messageMargin: 35,
        mirrorActors: false,
      },
    });
  }

  let modalInstance = null;
  let currentScale = 1.0;
  let currentTranslateX = 0;
  let currentTranslateY = 0;
  let isDragging = false;
  let startX = 0;
  let startY = 0;
  let baseFitScale = 1.0;

  function ensureMermaidModal() {
    if (modalInstance) return modalInstance;

    const modal = document.createElement('div');
    modal.id = 'mermaid-modal';
    modal.className = 'mermaid-modal';
    modal.setAttribute('role', 'dialog');
    modal.setAttribute('aria-modal', 'true');
    modal.setAttribute('aria-label', 'Mermaid Diagram Viewer');
    modal.style.display = 'none';

    modal.innerHTML = `
      <div class="mermaid-modal-backdrop"></div>
      <div class="mermaid-modal-container">
        <header class="mermaid-modal-toolbar">
          <div class="mermaid-modal-meta">
            <span class="mermaid-modal-title">Diagram Viewer</span>
            <span class="mermaid-modal-hint">Scroll to zoom • Drag to pan</span>
          </div>
          <div class="mermaid-modal-controls">
            <button type="button" class="mermaid-modal-btn mermaid-btn-out" title="Zoom Out (-)" aria-label="Zoom Out">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="5" y1="12" x2="19" y2="12"></line></svg>
            </button>
            <span class="mermaid-modal-zoom-val">100%</span>
            <button type="button" class="mermaid-modal-btn mermaid-btn-in" title="Zoom In (+)" aria-label="Zoom In">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>
            </button>
            <button type="button" class="mermaid-modal-btn mermaid-btn-fit" title="Reset View (0)" aria-label="Reset View">Fit</button>
            <button type="button" class="mermaid-modal-btn mermaid-btn-close" title="Close (Esc)" aria-label="Close">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
            </button>
          </div>
        </header>
        <div class="mermaid-modal-viewport">
          <div class="mermaid-modal-stage"></div>
        </div>
      </div>
    `;

    document.body.appendChild(modal);
    modalInstance = modal;

    const viewport = modal.querySelector('.mermaid-modal-viewport');
    const stage = modal.querySelector('.mermaid-modal-stage');
    const zoomVal = modal.querySelector('.mermaid-modal-zoom-val');
    const backdrop = modal.querySelector('.mermaid-modal-backdrop');

    function updateTransform() {
      stage.style.transform = `translate(${currentTranslateX}px, ${currentTranslateY}px) scale(${currentScale})`;
      const pct = Math.round((currentScale / baseFitScale) * 100);
      zoomVal.textContent = `${pct}%`;
    }

    function resetView() {
      currentScale = baseFitScale;
      currentTranslateX = 0;
      currentTranslateY = 0;
      updateTransform();
    }

    function zoomBy(factor, centerX, centerY) {
      const nextScale = Math.min(Math.max(currentScale * factor, baseFitScale * 0.25), baseFitScale * 8.0);
      if (centerX !== undefined && centerY !== undefined) {
        const ratio = nextScale / currentScale;
        currentTranslateX = centerX - (centerX - currentTranslateX) * ratio;
        currentTranslateY = centerY - (centerY - currentTranslateY) * ratio;
      }
      currentScale = nextScale;
      updateTransform();
    }

    modal.querySelector('.mermaid-btn-in').addEventListener('click', (e) => {
      e.stopPropagation();
      zoomBy(1.25);
    });

    modal.querySelector('.mermaid-btn-out').addEventListener('click', (e) => {
      e.stopPropagation();
      zoomBy(0.8);
    });

    modal.querySelector('.mermaid-btn-fit').addEventListener('click', (e) => {
      e.stopPropagation();
      resetView();
    });

    modal.querySelector('.mermaid-btn-close').addEventListener('click', (e) => {
      e.stopPropagation();
      closeMermaidModal();
    });

    backdrop.addEventListener('click', () => {
      closeMermaidModal();
    });

    viewport.addEventListener('wheel', (e) => {
      e.preventDefault();
      const rect = viewport.getBoundingClientRect();
      const cursorX = e.clientX - rect.left - rect.width / 2;
      const cursorY = e.clientY - rect.top - rect.height / 2;
      const factor = e.deltaY < 0 ? 1.15 : 0.87;
      zoomBy(factor, cursorX, cursorY);
    }, { passive: false });

    viewport.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      isDragging = true;
      startX = e.clientX - currentTranslateX;
      startY = e.clientY - currentTranslateY;
      viewport.setPointerCapture(e.pointerId);
      viewport.classList.add('is-dragging');
    });

    viewport.addEventListener('pointermove', (e) => {
      if (!isDragging) return;
      currentTranslateX = e.clientX - startX;
      currentTranslateY = e.clientY - startY;
      updateTransform();
    });

    const endDrag = (e) => {
      if (isDragging) {
        isDragging = false;
        viewport.classList.remove('is-dragging');
        try { viewport.releasePointerCapture(e.pointerId); } catch (_) {}
      }
    };

    viewport.addEventListener('pointerup', endDrag);
    viewport.addEventListener('pointercancel', endDrag);

    window.addEventListener('keydown', (e) => {
      if (modal.style.display !== 'flex') return;
      if (e.key === 'Escape') {
        e.preventDefault();
        closeMermaidModal();
      } else if (e.key === '+' || e.key === '=') {
        e.preventDefault();
        zoomBy(1.2);
      } else if (e.key === '-' || e.key === '_') {
        e.preventDefault();
        zoomBy(0.833);
      } else if (e.key === '0') {
        e.preventDefault();
        resetView();
      }
    });

    return modal;
  }

  function openMermaidModal(sourceSvg) {
    const modal = ensureMermaidModal();
    const stage = modal.querySelector('.mermaid-modal-stage');
    const viewport = modal.querySelector('.mermaid-modal-viewport');
    stage.innerHTML = '';

    // Clone source SVG
    const cloned = sourceSvg.cloneNode(true);

    // Determine intrinsic dimensions from viewBox or attributes
    let naturalWidth = 800;
    let naturalHeight = 600;

    const viewBoxAttr = sourceSvg.getAttribute('viewBox');
    if (viewBoxAttr) {
      const parts = viewBoxAttr.trim().split(/[\s,]+/);
      if (parts.length === 4) {
        naturalWidth = parseFloat(parts[2]) || 800;
        naturalHeight = parseFloat(parts[3]) || 600;
      }
    } else {
      const wAttr = parseFloat(sourceSvg.getAttribute('width'));
      const hAttr = parseFloat(sourceSvg.getAttribute('height'));
      if (wAttr && hAttr) {
        naturalWidth = wAttr;
        naturalHeight = hAttr;
      } else {
        const rect = sourceSvg.getBoundingClientRect();
        naturalWidth = rect.width || 800;
        naturalHeight = rect.height || 600;
      }
    }

    // Explicitly set concrete pixel dimensions to prevent 0px flex collapse
    cloned.setAttribute('width', naturalWidth);
    cloned.setAttribute('height', naturalHeight);
    cloned.style.setProperty('width', naturalWidth + 'px', 'important');
    cloned.style.setProperty('height', naturalHeight + 'px', 'important');
    cloned.style.setProperty('min-width', naturalWidth + 'px', 'important');
    cloned.style.setProperty('min-height', naturalHeight + 'px', 'important');
    cloned.style.setProperty('max-width', 'none', 'important');
    cloned.style.setProperty('max-height', 'none', 'important');
    cloned.style.setProperty('display', 'block', 'important');
    cloned.style.setProperty('visibility', 'visible', 'important');
    cloned.style.setProperty('opacity', '1', 'important');

    stage.appendChild(cloned);
    modal.style.display = 'flex';
    document.body.style.overflow = 'hidden';

    // Calculate initial fit scale immediately
    const vWidth = viewport.clientWidth || (window.innerWidth * 0.95);
    const vHeight = viewport.clientHeight || (window.innerHeight * 0.88);

    const scaleX = (vWidth - 80) / naturalWidth;
    const scaleY = (vHeight - 80) / naturalHeight;
    baseFitScale = Math.min(scaleX, scaleY, 1.25);
    if (!isFinite(baseFitScale) || baseFitScale <= 0.1) baseFitScale = 1.0;

    currentScale = baseFitScale;
    currentTranslateX = 0;
    currentTranslateY = 0;

    stage.style.transform = `translate(${currentTranslateX}px, ${currentTranslateY}px) scale(${currentScale})`;
    modal.querySelector('.mermaid-modal-zoom-val').textContent = `${Math.round((currentScale / baseFitScale) * 100)}%`;
  }

  function closeMermaidModal() {
    if (!modalInstance) return;
    modalInstance.style.display = 'none';
    document.body.style.overflow = '';
  }

  async function renderAllMermaid() {
    if (!window.mermaid) {
      setTimeout(renderAllMermaid, 50);
      return;
    }

    // CRITICAL: Astro / Starlight injects Expressive Code's stylesheet (<link rel="stylesheet">)
    // and client module script (<script>) inside the VERY FIRST .expressive-code container on the page.
    // If that container is a Mermaid diagram and we remove/replace it, the entire page loses Expressive Code
    // styles, terminal headers, and copy buttons!
    // We immediately relocate all Expressive Code links and scripts into document.head so they are permanently preserved.
    document.querySelectorAll('link[href*="ec."], script[src*="ec."]').forEach(asset => {
      if (asset.parentElement !== document.head) {
        document.head.appendChild(asset);
      }
    });

    initMermaidTheme();

    const pres = document.querySelectorAll('pre[data-language="mermaid"], .language-mermaid');
    if (!pres.length) return;

    let idx = 0;
    for (const pre of pres) {
      // Find the outermost expressive-code wrapper or figure
      const container = pre.closest('.expressive-code') || pre.closest('figure') || pre;

      // Extract raw code lines
      let raw = '';
      const ecLines = pre.querySelectorAll('.ec-line');
      if (ecLines.length > 0) {
        raw = Array.from(ecLines).map(line => line.textContent || '').join('\n');
      } else {
        raw = pre.textContent || '';
      }

      // Decode any HTML entities (&lt;, &#x3C;, &gt;, &quot;, &amp;)
      let cleanCode = decodeHtmlEntities(raw).trim();
      if (!cleanCode) continue;

      const id = `mermaid-svg-${Date.now()}-${idx++}`;
      try {
        const { svg } = await window.mermaid.render(id, cleanCode);
        const wrapper = document.createElement('div');
        wrapper.className = 'mermaid-wrapper not-content';
        wrapper.setAttribute('title', 'Click to expand diagram in interactive zoom viewer');
        wrapper.innerHTML = svg;

        // Add visual zoom badge in top right corner
        const badge = document.createElement('button');
        badge.type = 'button';
        badge.className = 'mermaid-zoom-badge';
        badge.setAttribute('aria-label', 'Expand and zoom diagram');
        badge.innerHTML = `
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="8"></circle>
            <line x1="21" y1="21" x2="16.65" y2="16.65"></line>
            <line x1="11" y1="8" x2="11" y2="14"></line>
            <line x1="8" y1="11" x2="14" y2="11"></line>
          </svg>
          <span>Expand</span>
        `;
        wrapper.appendChild(badge);

        // Click handler to open zoom modal
        wrapper.addEventListener('click', (e) => {
          e.preventDefault();
          const svgEl = wrapper.querySelector('svg');
          if (svgEl) {
            openMermaidModal(svgEl);
          }
        });

        // Preserve any remaining links or scripts in container
        const embeddedAssets = container.querySelectorAll('link, script');
        embeddedAssets.forEach(asset => {
          document.head.appendChild(asset);
        });

        container.replaceWith(wrapper);

        // Adjust SVG viewBox with generous padding to prevent title and cluster cutoff
        const svgEl = wrapper.querySelector('svg');
        if (svgEl) {
          adjustSvgViewBox(svgEl);
        }
      } catch (err) {
        console.error('Failed to render Mermaid diagram:', err);
      }
    }
  }

  function adjustSvgViewBox(svgEl) {
    if (!svgEl) return;
    // Only adjust flowcharts that contain clusters (subgraphs) where titles might be clipped
    if (!svgEl.querySelector('.cluster')) return;
    // Prevent repeated or compounding adjustments
    if (svgEl.dataset.viewboxAdjusted === 'true') return;
    svgEl.dataset.viewboxAdjusted = 'true';

    // 1. Ensure foreignObject and text containers do not clip text internally
    svgEl.querySelectorAll('foreignObject, foreignObject > div').forEach(el => {
      el.style.overflow = 'visible';
    });

    // 2. Read existing viewBox values
    const vbAttr = svgEl.getAttribute('viewBox');
    if (!vbAttr) return;

    const parts = vbAttr.trim().split(/[\s,]+/).map(Number);
    if (parts.length !== 4 || parts.some(n => isNaN(n))) return;

    const [origMinX, origMinY, origWidth, origHeight] = parts;

    // 3. Measure actual rendered geometric bounding box if available
    let bBox = null;
    try {
      bBox = svgEl.getBBox();
    } catch (_) {}

    let leftOverhang = 0;
    let topOverhang = 0;
    let rightOverhang = 0;
    let bottomOverhang = 0;

    if (bBox && bBox.width > 0 && bBox.height > 0) {
      if (bBox.x < origMinX) {
        leftOverhang = origMinX - bBox.x;
      }
      if (bBox.y < origMinY) {
        topOverhang = origMinY - bBox.y;
      }
      const bBoxMaxX = bBox.x + bBox.width;
      const origMaxX = origMinX + origWidth;
      if (bBoxMaxX > origMaxX) {
        rightOverhang = bBoxMaxX - origMaxX;
      }
      const bBoxMaxY = bBox.y + bBox.height;
      const origMaxY = origMinY + origHeight;
      if (bBoxMaxY > origMaxY) {
        bottomOverhang = bBoxMaxY - origMaxY;
      }
    }

    // Add modest safety padding (at least 16px horizontal, 14px vertical)
    const padLeft = Math.ceil(Math.max(16, leftOverhang + 12));
    const padTop = Math.ceil(Math.max(14, topOverhang + 10));
    const padRight = Math.ceil(Math.max(16, rightOverhang + 12));
    const padBottom = Math.ceil(Math.max(14, bottomOverhang + 10));

    const finalMinX = Math.floor(origMinX - padLeft);
    const finalMinY = Math.floor(origMinY - padTop);
    const finalWidth = Math.ceil(origWidth + padLeft + padRight);
    const finalHeight = Math.ceil(origHeight + padTop + padBottom);

    svgEl.setAttribute('viewBox', `${finalMinX} ${finalMinY} ${finalWidth} ${finalHeight}`);
    svgEl.style.maxWidth = `${finalWidth}px`;
    svgEl.style.width = '100%';
    svgEl.style.height = 'auto';
    svgEl.style.overflow = 'visible';
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', renderAllMermaid);
  } else {
    renderAllMermaid();
  }

  document.addEventListener('astro:page-load', renderAllMermaid);
})();
