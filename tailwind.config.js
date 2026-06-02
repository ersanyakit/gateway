module.exports = {
  content: [
    './views/**/*.html'
  ],
  theme: {
    extend: {
      colors: {
        ink: '#131313',
        muted: 'rgba(19, 19, 19, 0.63)',
        line: 'rgba(19, 19, 19, 0.08)',
        surface: '#ffffff',
        wash: '#f9f9f9',
        accent: '#ff37c7',
        accentHover: '#e500a5'
      },
      boxShadow: {
        soft: '0 20px 52px rgba(19, 19, 19, 0.08)',
        glow: '0 16px 44px rgba(255, 55, 199, 0.18)'
      }
    }
  }
}
