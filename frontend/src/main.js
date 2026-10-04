import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import SearchView from './views/SearchView.vue'
import RegisterView from './views/RegisterView.vue'
import ItemDetailView from './views/ItemDetailView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: SearchView },
    { path: '/register', component: RegisterView },
    { path: '/items/:id', component: ItemDetailView }
  ]
})

createApp(App).use(router).mount('#app')
