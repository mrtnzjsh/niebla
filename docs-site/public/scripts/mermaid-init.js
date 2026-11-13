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
    });
  }

  async function renderAllMermaid() {
    if (!window.mermaid) {
      setTimeout(renderAllMermaid, 50);
      return;
    }

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
        wrapper.innerHTML = svg;
        container.replaceWith(wrapper);
      } catch (err) {
        console.error('Failed to render Mermaid diagram:', err);
      }
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', renderAllMermaid);
  } else {
    renderAllMermaid();
  }

  document.addEventListener('astro:page-load', renderAllMermaid);
})();
