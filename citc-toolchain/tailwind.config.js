/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./internal/web/views/**/*.templ",
    "./internal/web/views/**/*_templ.go",
    "./internal/web/**/*.go",
  ],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        critique: {
          bg: '#090d16',
          sidebar: '#0d1322',
          card: '#111827',
          border: '#1f293d',
          hover: '#1a2438',
          accent: '#38bdf8',
          blue: '#2563eb',
        }
      },
      fontFamily: {
        mono: ['JetBrains Mono', 'Fira Code', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      }
    },
  },
  plugins: [],
}
