#!/usr/bin/env node
/**
 * Offline Documentation & Asset Integrity Validator
 * --------------------------------------------------
 * Runs against the compiled `dist/` output of docs-site to ensure:
 * 1. Zero broken internal links (<a href="...">)
 * 2. Zero broken internal anchor fragments (<a href="#anchor">)
 * 3. Zero missing local images/assets (<img src="...">)
 * 4. Zero unrendered or broken Mermaid code blocks
 *
 * Runs with zero external npm dependencies (pure Node.js built-ins).
 */

const fs = require('fs');
const path = require('path');

const DIST_DIR = path.resolve(__dirname, '../dist');

if (!fs.existsSync(DIST_DIR)) {
  console.error(`❌ Error: dist directory not found at ${DIST_DIR}. Run 'astro build' first.`);
  process.exit(1);
}

// Helper: collect all HTML files recursively
function getHtmlFiles(dir) {
  let results = [];
  const list = fs.readdirSync(dir);
  for (const file of list) {
    const fullPath = path.join(dir, file);
    const stat = fs.statSync(fullPath);
    if (stat && stat.isDirectory()) {
      results = results.concat(getHtmlFiles(fullPath));
    } else if (file.endsWith('.html')) {
      results.push(fullPath);
    }
  }
  return results;
}

const htmlFiles = getHtmlFiles(DIST_DIR);
console.log(`🔍 Found ${htmlFiles.length} HTML pages in dist/ to analyze...`);

let totalLinksChecked = 0;
let totalAnchorsChecked = 0;
let totalImagesChecked = 0;
let totalMermaidChecked = 0;
const errors = [];

const BASE_PATH = '/niebla';

function normalizeInternalPath(p) {
  let clean = p.replace(/^\/+/, '');
  const baseClean = BASE_PATH.replace(/^\/+/, '');
  if (clean.startsWith(baseClean)) {
    clean = clean.substring(baseClean.length).replace(/^\/+/, '');
  }
  return clean;
}

const fileIdsCache = new Map();

function getFileIds(filePath) {
  if (fileIdsCache.has(filePath)) {
    return fileIdsCache.get(filePath);
  }
  const content = fs.readFileSync(filePath, 'utf8');
  const ids = new Set();
  const idRegex = /\bid=["']([^"']+)["']/g;
  let match;
  while ((match = idRegex.exec(content)) !== null) {
    ids.add(match[1]);
  }
  fileIdsCache.set(filePath, ids);
  return ids;
}

for (const htmlFile of htmlFiles) {
  const relHtml = path.relative(DIST_DIR, htmlFile);
  const content = fs.readFileSync(htmlFile, 'utf8');

  // 1. Check Mermaid diagrams
  if (content.includes('mermaid-error') || content.includes('Syntax error in text') || content.includes('mermaid-err')) {
    errors.push({
      file: relHtml,
      type: 'MERMAID_ERROR',
      detail: 'Found syntax error banner or unrendered Mermaid diagram error in HTML'
    });
  }
  const mermaidMatches = content.match(/class=["'][^"']*mermaid[^"']*["']/g);
  if (mermaidMatches) {
    totalMermaidChecked += mermaidMatches.length;
  }

  // 2. Check <a> links
  const linkRegex = /<a\s+[^>]*href=["']([^"']+)["']/gi;
  let linkMatch;
  while ((linkMatch = linkRegex.exec(content)) !== null) {
    const href = linkMatch[1].trim();

    // Skip mailto, tel, javascript, external http(s)
    if (href.startsWith('http://') || href.startsWith('https://') || href.startsWith('mailto:') || href.startsWith('tel:') || href.startsWith('javascript:')) {
      continue;
    }

    totalLinksChecked++;
    let targetPath = '';
    let targetAnchor = '';

    if (href.startsWith('#')) {
      // Local anchor on same page
      targetPath = htmlFile;
      targetAnchor = href.substring(1);
    } else {
      const [pathPart, anchorPart] = href.split('#');
      targetAnchor = anchorPart || '';

      if (pathPart.startsWith('/')) {
        // Absolute from base path / dist
        const cleanPath = normalizeInternalPath(pathPart);
        if (cleanPath === '' || cleanPath.endsWith('/')) {
          targetPath = path.join(DIST_DIR, cleanPath, 'index.html');
        } else if (cleanPath.endsWith('.html')) {
          targetPath = path.join(DIST_DIR, cleanPath);
        } else {
          // Could be /foo -> /foo/index.html or /foo.html
          const dirIndex = path.join(DIST_DIR, cleanPath, 'index.html');
          const directHtml = path.join(DIST_DIR, cleanPath + '.html');
          if (fs.existsSync(dirIndex)) {
            targetPath = dirIndex;
          } else if (fs.existsSync(directHtml)) {
            targetPath = directHtml;
          } else {
            targetPath = dirIndex;
          }
        }
      } else {
        // Relative link
        const currentDir = path.dirname(htmlFile);
        targetPath = path.resolve(currentDir, pathPart);
        if (fs.existsSync(targetPath) && fs.statSync(targetPath).isDirectory()) {
          targetPath = path.join(targetPath, 'index.html');
        }
      }
    }

    // Verify file exists
    if (!fs.existsSync(targetPath)) {
      errors.push({
        file: relHtml,
        type: 'BROKEN_LINK',
        detail: `Link href="${href}" -> missing target file: ${path.relative(DIST_DIR, targetPath)}`
      });
      continue;
    }

    // Verify anchor if present
    if (targetAnchor) {
      totalAnchorsChecked++;
      const ids = getFileIds(targetPath);
      // Also check decoded URI anchor (e.g. %20 -> space)
      const decodedAnchor = decodeURIComponent(targetAnchor);
      if (!ids.has(targetAnchor) && !ids.has(decodedAnchor)) {
        errors.push({
          file: relHtml,
          type: 'BROKEN_ANCHOR',
          detail: `Anchor #${targetAnchor} not found in ${path.relative(DIST_DIR, targetPath)}`
        });
      }
    }
  }

  // 3. Check <img> sources
  const imgRegex = /<img\s+[^>]*src=["']([^"']+)["']/gi;
  let imgMatch;
  while ((imgMatch = imgRegex.exec(content)) !== null) {
    const src = imgMatch[1].trim();

    // Skip data URIs or external images
    if (src.startsWith('data:') || src.startsWith('http://') || src.startsWith('https://')) {
      continue;
    }

    totalImagesChecked++;
    let imgPath = '';
    if (src.startsWith('/')) {
      imgPath = path.join(DIST_DIR, normalizeInternalPath(src));
    } else {
      imgPath = path.resolve(path.dirname(htmlFile), src);
    }

    if (!fs.existsSync(imgPath)) {
      errors.push({
        file: relHtml,
        type: 'MISSING_IMAGE',
        detail: `Image src="${src}" -> missing file: ${path.relative(DIST_DIR, imgPath)}`
      });
    }
  }
}

console.log(`\n=== Docs Site Verification Summary ===`);
console.log(`Pages Audited      : ${htmlFiles.length}`);
console.log(`Links Verified     : ${totalLinksChecked}`);
console.log(`Anchors Verified   : ${totalAnchorsChecked}`);
console.log(`Images Verified    : ${totalImagesChecked}`);
console.log(`Mermaid Blocks     : ${totalMermaidChecked}`);

if (errors.length > 0) {
  console.error(`\n❌ Found ${errors.length} documentation integrity error(s):`);
  for (const err of errors) {
    console.error(`  - [${err.type}] in ${err.file}: ${err.detail}`);
  }
  process.exit(1);
}

console.log(`\n✅ All documentation pages, links, anchors, and images verified successfully!`);
process.exit(0);
