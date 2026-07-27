<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const activeAgent = ref('claude')
const prompt = ref('')
const isLoading = ref(false)

const messages = ref<{id: number, role: string, agent: string, content: string}[]>([])

let eventSource: EventSource | null = null

onMounted(() => {
  // Initialize SSE connection to Go backend
  eventSource = new EventSource('http://localhost:8080/api/events')

  eventSource.onmessage = (event) => {
    console.log('SSE message:', event.data)
  }

  eventSource.addEventListener('connected', (event) => {
    const data = JSON.parse(event.data)
    console.log('SSE Connected:', data)
    messages.value.push({
      id: Date.now(),
      role: 'assistant',
      agent: 'system',
      content: 'System is online and connected via SSE.'
    })
  })

  eventSource.addEventListener('command_started', (event) => {
    console.log('Command started:', event.data)
  })

  eventSource.addEventListener('command_result', (event) => {
    isLoading.value = false
    messages.value.push({
      id: Date.now(),
      role: 'assistant',
      agent: activeAgent.value,
      content: event.data
    })
  })

  eventSource.addEventListener('command_error', (event) => {
    isLoading.value = false
    messages.value.push({
      id: Date.now(),
      role: 'assistant',
      agent: 'system',
      content: `Error: ${event.data}`
    })
  })
})

onUnmounted(() => {
  if (eventSource) {
    eventSource.close()
  }
})

const sendCommand = async () => {
  if (!prompt.value.trim() || isLoading.value) return

  const userText = prompt.value
  messages.value.push({
    id: Date.now(),
    role: 'user',
    agent: 'user',
    content: userText
  })

  prompt.value = ''
  isLoading.value = true

  try {
    const res = await fetch('http://localhost:8080/api/command', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        provider: activeAgent.value,
        prompt: userText
      })
    })

    if (!res.ok) {
      throw new Error(`Failed to send: ${res.statusText}`)
    }
  } catch (err) {
    isLoading.value = false
    messages.value.push({
      id: Date.now(),
      role: 'assistant',
      agent: 'system',
      content: `Network Error: ${(err as Error).message}`
    })
  }
}

// Claude's distinct star icon
const ClaudeIcon = `<svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg"><path fill-rule="evenodd" clip-rule="evenodd" d="M11.238 2.378a.862.862 0 0 1 1.524 0l1.79 3.513 3.974.606a.862.862 0 0 1 .471 1.45l-2.822 2.836.634 3.968a.862.862 0 0 1-1.233.916L12 14.137l-3.576 1.53a.862.862 0 0 1-1.233-.916l.634-3.968-2.822-2.836a.862.862 0 0 1 .471-1.45l3.974-.606 1.79-3.513Z" fill="currentColor"/></svg>`
</script>

<template>
  <div class="flex h-screen w-full bg-[#0A0A0A] text-gray-100 overflow-hidden">
    
    <!-- Sidebar (Visible on Desktop) -->
    <aside class="hidden md:flex flex-col w-64 border-r border-[#1F1F1F] bg-[#0A0A0A] shrink-0">
      <div class="h-14 flex items-center px-4 border-b border-[#1F1F1F]">
        <h1 class="text-lg font-medium tracking-tight">Holocron</h1>
      </div>
      <div class="flex-1 overflow-y-auto p-3 space-y-1">
        <div class="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3 px-2 mt-2">AI Agents</div>
        <button 
          v-for="agent in ['claude', 'gemini', 'antigravity', 'codex']" 
          :key="agent"
          @click="activeAgent = agent"
          class="w-full flex items-center gap-3 px-3 py-2.5 rounded-xl transition-all duration-200 text-left"
          :class="activeAgent === agent ? 'bg-[#1F1F1F] text-gray-100 shadow-sm' : 'text-gray-400 hover:bg-[#1F1F1F]/50 hover:text-gray-200'"
        >
          <div 
            class="w-6 h-6 rounded flex items-center justify-center transition-colors"
            :class="activeAgent === agent ? 'bg-[#4B9CD3] text-white shadow-sm shadow-[#4B9CD3]/20' : 'bg-[#141414] text-gray-500'"
          >
            <svg v-if="agent === 'claude'" class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24" v-html="ClaudeIcon"></svg>
            <div v-else class="w-2.5 h-2.5 rounded-full bg-current"></div>
          </div>
          <span class="font-medium capitalize">{{ agent }}</span>
        </button>
      </div>
    </aside>

    <!-- Main Chat Interface -->
    <main class="flex-1 flex flex-col relative h-full bg-[#0A0A0A]">
      
      <!-- Top Header -->
      <header class="h-14 flex items-center justify-between px-4 sticky top-0 bg-[#0A0A0A]/90 backdrop-blur-md z-20 md:border-b-0 border-b border-[#1F1F1F]">
        <!-- Mobile menu toggle -->
        <button class="p-2 -ml-2 text-gray-400 hover:text-gray-100 transition-colors md:hidden">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="3" y1="12" x2="21" y2="12"></line>
            <line x1="3" y1="6" x2="21" y2="6"></line>
            <line x1="3" y1="18" x2="21" y2="18"></line>
          </svg>
        </button>

        <h1 class="text-[17px] font-medium tracking-tight md:hidden">Holocron</h1>

        <div class="hidden md:flex items-center gap-2">
          <span class="text-gray-500 text-sm">Chatting with</span>
          <span class="font-medium capitalize text-gray-200">{{ activeAgent }}</span>
        </div>

        <button class="p-2 -mr-2 text-gray-400 hover:text-gray-100 transition-colors">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 5v14M5 12h14"></path>
          </svg>
        </button>
      </header>

      <!-- Chat History -->
      <div class="flex-1 overflow-y-auto px-4 py-6">
        <div class="max-w-3xl mx-auto space-y-8">
          
          <div v-for="msg in messages" :key="msg.id" class="flex flex-col w-full">
            <!-- User Message (Bubble on right) -->
            <div v-if="msg.role === 'user'" class="flex justify-end w-full">
              <div class="bg-[#1F1F1F] text-gray-100 px-4 py-3 rounded-2xl max-w-[85%] text-[16px] leading-[1.5] shadow-sm border border-white/5">
                {{ msg.content }}
              </div>
            </div>
            
            <!-- AI Message (Flush left with icon) -->
            <div v-else class="flex gap-4 w-full">
              <div class="w-7 h-7 shrink-0 rounded bg-[#4B9CD3] text-white flex items-center justify-center mt-0.5 shadow-sm shadow-[#4B9CD3]/20" v-html="ClaudeIcon"></div>
              <div class="flex-1 space-y-4 text-[16px] leading-[1.6] text-gray-200 prose prose-invert prose-p:leading-[1.6] max-w-none">
                <p>{{ msg.content }}</p>
              </div>
            </div>
          </div>
          
        </div>
      </div>

      <!-- Input Area -->
      <div class="p-4 bg-gradient-to-t from-[#0A0A0A] via-[#0A0A0A] to-transparent shrink-0">
        <div class="max-w-3xl mx-auto relative flex items-end bg-[#141414] border border-white/5 rounded-[24px] px-2 py-2 pb-2 min-h-[52px] shadow-lg">
          
          <!-- Attachment Button -->
          <button class="p-2 text-gray-500 hover:text-[#4B9CD3] shrink-0 transition-colors mb-0.5">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"></path>
            </svg>
          </button>
          
          <!-- Textarea -->
          <textarea 
            v-model="prompt"
            rows="1"
            placeholder="Message Holocron..."
            class="flex-1 bg-transparent text-gray-100 placeholder-gray-600 focus:outline-none resize-none py-2.5 px-2 max-h-32 min-h-[24px] text-[16px] leading-[1.4]"
            style="line-height: 1.4;"
            @keydown.enter.prevent="sendCommand"
          ></textarea>
          
          <!-- Send Button (Up Arrow inside rounded square) -->
          <button 
            @click="sendCommand"
            class="w-8 h-8 rounded-full bg-[#4B9CD3] hover:bg-[#5CAEE5] text-white flex items-center justify-center shrink-0 mb-0.5 transition-colors disabled:opacity-50 disabled:bg-[#2A2A2A] disabled:text-gray-600 shadow-md shadow-[#4B9CD3]/20"
            :disabled="!prompt.trim() || isLoading"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <line x1="12" y1="19" x2="12" y2="5"></line>
              <polyline points="5 12 12 5 19 12"></polyline>
            </svg>
          </button>

        </div>
      </div>
    </main>
  </div>
</template>

<style>
/* Hide scrollbar for cleaner aesthetic */
.overflow-y-auto {
  scrollbar-width: none; /* Firefox */
  -ms-overflow-style: none;  /* IE and Edge */
}
.overflow-y-auto::-webkit-scrollbar {
  display: none; /* Chrome, Safari and Opera */
}
</style>
