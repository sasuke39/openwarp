import { h } from 'vue'
import DefaultTheme from 'vitepress/theme'
import ProductHome from './ProductHome.vue'
import './tokens.css'
import './home.css'

export default {
  extends: DefaultTheme,
  Layout: () => h(DefaultTheme.Layout, null, {
    'home-hero-before': () => h(ProductHome)
  })
}
