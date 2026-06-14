// 主入口文件
import { Router } from './utils/router.js';
import { ApiConnectionService } from './services/apiConnection.js';
import { ApiConfigService } from './services/apiConfig.js';
import { Sidebar } from './components/sidebar.js';
import { ApiCard } from './components/apiCard.js';
import { isMobile } from './utils/deviceUtils.js';

// 全局导出setupCardExpansionForCard函数
window.setupCardExpansionForCard = function (card) {
    const expandBtn = card.querySelector('.expand-card-btn');
    const collapseBtn = card.querySelector('.collapse-card-btn');

    if (expandBtn && collapseBtn) {
        // 检查卡片高度是否超过限制
        setTimeout(() => {
            // 获取卡片内容的实际高度
            const cardContent = card.querySelector('.card-content');
            if (cardContent && cardContent.scrollHeight > 350) {
                expandBtn.style.display = 'block';
            } else {
                expandBtn.style.display = 'none';
                collapseBtn.style.display = 'none';
                // 如果内容不需要折叠，移除渐变遮罩效果
                card.classList.add('no-gradient');
            }
        }, 200);

        // 展开按钮点击事件
        expandBtn.addEventListener('click', () => {
            card.classList.add('expanded');
            expandBtn.style.display = 'none';
            collapseBtn.style.display = 'block';
        });

        // 折叠按钮点击事件
        collapseBtn.addEventListener('click', () => {
            card.classList.remove('expanded');
            expandBtn.style.display = 'block';
            collapseBtn.style.display = 'none';
            // 滚动到卡片顶部
            card.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
    }
};

document.addEventListener('DOMContentLoaded', function () {
    // 初始化服务
    const apiConnection = new ApiConnectionService();
    const apiConfig = new ApiConfigService(apiConnection);
    const sidebar = new Sidebar();

    // 获取DOM元素
    const connectionSettings = document.getElementById('connection-settings');
    const apiUrlInput = document.getElementById('api-url');
    const apiKeyInput = document.getElementById('api-key');
    const connectButton = document.getElementById('connect-button');
    const apiConfigArea = document.getElementById('api-config-area');
    const content = document.getElementById('content');
    const review00EnabledInput = document.getElementById('review00-enabled');
    const review00BaseUrlInput = document.getElementById('review00-base-url');
    const review00ApiKeyInput = document.getElementById('review00-api-key');
    const review00Form = document.getElementById('request-review-form');
    const review00SaveButton = document.getElementById('review00-save-button');
    const review00TestButton = document.getElementById('review00-test-button');
    const review00Status = document.getElementById('review00-status');

    // 初始化主题切换
    initThemeToggle();

    // 初始化路由
    const router = new Router({
        defaultRoute: () => {
            const apiContainer = document.getElementById('api-container');
            apiContainer.style.display = '';

            if (apiConnection.connection.isConnected) {
                connectionSettings.style.display = 'none';
                apiConfigArea.style.display = 'block';
                loadApiConfig();
            } else if (apiConnection.connection.url && apiConnection.connection.key) {
                connectionSettings.style.display = 'none';
                apiConfigArea.style.display = 'block';
                testConnectionAndLoadConfig();
            } else {
                connectionSettings.style.display = 'block';
                apiConfigArea.style.display = 'none';
            }
        }
    });

    // 设置路由
    router.addRoute('settings', () => {
        const apiContainer = document.getElementById('api-container');
        apiContainer.style.display = '';

        if (apiConnection.connection.isConnected) {
            connectionSettings.style.display = 'none';
            apiConfigArea.style.display = 'block';
            loadApiConfig();
        } else if (apiConnection.connection.url && apiConnection.connection.key) {
            connectionSettings.style.display = 'none';
            apiConfigArea.style.display = 'block';
            testConnectionAndLoadConfig();
        } else {
            connectionSettings.style.display = 'block';
            apiConfigArea.style.display = 'none';
        }
    });

    // 初始化连接表单
    if (apiConnection.connection.url) {
        apiUrlInput.value = apiConnection.connection.url;
    }
    if (apiConnection.connection.key) {
        apiKeyInput.value = apiConnection.connection.key;
    }

    // 修改：将点击事件改为表单提交事件
    const connectionForm = document.getElementById('connection-form');
    if (connectionForm) {
        connectionForm.addEventListener('submit', async (e) => {
            e.preventDefault(); // 阻止表单默认提交行为

            // 获取输入的值
            const url = apiUrlInput.value.trim();
            const key = apiKeyInput.value.trim();

            // 验证输入
            if (!url) {
                alert('请输入服务器URL');
                return;
            }

            if (!key) {
                alert('请输入API Key');
                return;
            }

            // 显示加载状态
            connectButton.disabled = true;
            connectButton.innerHTML = '<span class="loading-spinner"></span> 连接中...';

            // 保存连接信息
            apiConnection.saveConnection(url, key);

            // 使用已有的测试连接函数
            await testConnectionAndLoadConfig();
        });
    }

    // 加载API配置
    async function loadApiConfig() {
        try {
            const apiContainer = document.getElementById('api-container');
            const apiCardsContainer = document.getElementById('api-cards-container');

            apiContainer.style.display = '';
            apiCardsContainer.innerHTML = '<div class="loading">加载提供商配置中...</div>';

            const data = await apiConfig.fetchConfig();
            apiCardsContainer.innerHTML = '';

            if (data.api_config && data.api_config.providers) {
                data.api_config.providers.forEach(provider => {
                    const apiCard = new ApiCard(provider);
                    apiCardsContainer.appendChild(apiCard.createCard());
                });
            }
            hydrateRequestReviewSettings(data.api_config || {});

            setupAddButton();
            setupSaveButton();
            setupRequestReviewForm();
            setupCardExpansion(); // 初始化卡片折叠功能

            // 添加窗口大小变化监听，更新卡片显示状态
            window.addEventListener('resize', () => {
                setupCardExpansion();
            });
        } catch (error) {
            console.error('加载API配置失败:', error);
            const apiCardsContainer = document.getElementById('api-cards-container');
            apiCardsContainer.innerHTML = `<div class="error">加载配置失败: ${error.message}</div>`;
        }
    }

    // 设置添加按钮
    function setupAddButton() {
        const addButton = document.getElementById('add-api-button');
        const newButton = addButton.cloneNode(true);
        addButton.parentNode.replaceChild(newButton, addButton);

        newButton.addEventListener('click', () => {
            const apiCard = new ApiCard();
            const card = apiCard.createCard();
            document.getElementById('api-cards-container').appendChild(card);

            // 为新卡片设置折叠功能
            setupCardExpansionForCard(card);
        });
    }

    // 设置保存按钮
    function setupSaveButton() {
        const saveButton = document.getElementById('save-config-button');
        if (saveButton && saveButton.dataset.initialized !== 'true') {
            saveButton.dataset.initialized = 'true';
            saveButton.addEventListener('click', async () => {
                const configData = collectApiConfigData();
                saveButton.disabled = true;

                try {
                    await apiConfig.saveConfig(configData);

                    // 使用模板更新按钮内容
                    const successTemplate = document.getElementById('save-success-template');
                    saveButton.innerHTML = successTemplate.innerHTML;

                    // 添加成功状态类
                    saveButton.classList.add('success');

                    // 1秒后恢复原始状态
                    setTimeout(() => {
                        saveButton.classList.remove('success');
                        const defaultTemplate = document.getElementById('save-default-template');
                        saveButton.innerHTML = defaultTemplate.innerHTML;
                        saveButton.disabled = false;
                    }, 1000);

                } catch (error) {
                    console.error('保存配置失败:', error);
                    alert(`保存配置失败: ${error.message}`);
                    saveButton.disabled = false;
                }
            });
        }
    }

    function hydrateRequestReviewSettings(apiConfigData) {
        const preferences = apiConfigData.preferences || {};
        const requestReview = preferences.request_review || preferences.review00 || {};
        review00EnabledInput.checked = requestReview.enabled !== false && Boolean(requestReview.base_url && requestReview.api_key);
        review00BaseUrlInput.value = requestReview.base_url || '';
        review00ApiKeyInput.value = requestReview.api_key || '';
        review00ApiKeyInput.dataset.configured = requestReview.api_key ? 'true' : '';
        setReview00Status(requestReview.base_url && requestReview.api_key ? '已配置 review00 投递。' : '未配置 review00。', 'neutral');
    }

    function collectRequestReviewSettings() {
        return {
            preferences: {
                request_review: {
                    enabled: review00EnabledInput.checked,
                    base_url: review00BaseUrlInput.value.trim()
                }
            }
        };
    }

    function setReview00Status(message, tone = 'neutral') {
        if (!review00Status) return;
        review00Status.textContent = message || '';
        review00Status.className = `request-review-status ${tone}`;
    }

    function setupRequestReviewForm() {
        if (!review00Form || review00Form.dataset.initialized === 'true') return;
        review00Form.dataset.initialized = 'true';

        review00Form.addEventListener('submit', async (event) => {
            event.preventDefault();
            const configData = collectRequestReviewSettings();
            const reviewConfig = configData.preferences.request_review;
            const apiKey = review00ApiKeyInput.value.trim();
            if (apiKey) {
                reviewConfig.api_key = apiKey;
            }

            if (reviewConfig.enabled && (!reviewConfig.base_url || (!apiKey && !review00ApiKeyInput.dataset.configured))) {
                setReview00Status('启用审查前需要填写 Base URL 和 API Key。', 'error');
                return;
            }

            review00SaveButton.disabled = true;
            setReview00Status('保存中...', 'neutral');
            try {
                await apiConfig.saveConfig(configData);
                setReview00Status('审查设置已保存。', 'success');
            } catch (error) {
                console.error('保存审查设置失败:', error);
                setReview00Status(`保存失败: ${error.message}`, 'error');
            } finally {
                review00SaveButton.disabled = false;
            }
        });

        review00TestButton.addEventListener('click', async () => {
            const configData = collectRequestReviewSettings();
            const reviewConfig = configData.preferences.request_review;
            const apiKey = review00ApiKeyInput.value.trim();
            if (!reviewConfig.base_url || !apiKey) {
                setReview00Status('测试连接前需要填写 Base URL 和 API Key。', 'error');
                return;
            }

            review00TestButton.disabled = true;
            setReview00Status('测试连接中...', 'neutral');
            try {
                const response = await apiConfig.testRequestReview({
                    preferences: {
                        request_review: {
                            enabled: review00EnabledInput.checked,
                            base_url: reviewConfig.base_url,
                            api_key: apiKey
                        }
                    }
                });
                setReview00Status(`review00 测试已排队：${response.message || 'ok'}`, 'success');
            } catch (error) {
                console.error('测试 review00 失败:', error);
                setReview00Status(`连接失败: ${error.message}`, 'error');
            } finally {
                review00TestButton.disabled = false;
            }
        });
    }

    // 初始化卡片折叠功能
    function setupCardExpansion() {
        document.querySelectorAll('.api-card:not(.template)').forEach(card => {
            setupCardExpansionForCard(card);
        });
    }

    // 主题切换功能
    function initThemeToggle() {
        const themeToggleBtn = document.getElementById('themeToggle');
        if (themeToggleBtn) {
            // 检查本地存储中的主题设置
            const savedTheme = localStorage.getItem('theme');
            if (savedTheme) {
                document.body.className = savedTheme;
            } else {
                // 检查系统主题偏好
                if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) {
                    document.body.className = 'dark-mode';
                    localStorage.setItem('theme', 'dark-mode');
                }
            }

            // 切换主题
            themeToggleBtn.addEventListener('click', () => {
                if (document.body.classList.contains('dark-mode')) {
                    document.body.classList.remove('dark-mode');
                    document.body.classList.add('light-mode');
                    localStorage.setItem('theme', 'light-mode');
                } else {
                    document.body.classList.remove('light-mode');
                    document.body.classList.add('dark-mode');
                    localStorage.setItem('theme', 'dark-mode');
                }

                // 切换主题时更新卡片折叠状态
                setTimeout(() => {
                    setupCardExpansion();
                }, 100);
            });
        }
    }

    // 收集API配置数据
    function collectApiConfigData() {
        const providers = [];
        document.querySelectorAll('.api-card:not(.template)').forEach(card => {
            const provider = {
                provider: card.querySelector('.provider-name').value,
                base_url: card.querySelector('.base-url').value,
                tools: card.querySelector('.tools-checkbox').checked
            };

            // 收集API密钥
            const apiKeys = Array.from(card.querySelectorAll('.api-key-entry .api-key'))
                .map(input => input.value.trim())
                .filter(key => key);

            provider.api = apiKeys.length === 1 ? apiKeys[0] : apiKeys;

            // 收集模型
            provider.model = Array.from(card.querySelectorAll('.model-entry'))
                .map(entry => {
                    const originalName = entry.querySelector('.original-model-name').value;
                    const renamedName = entry.querySelector('.renamed-model-name').value;

                    if (originalName) {
                        return renamedName ? { [originalName]: renamedName } : originalName;
                    }
                })
                .filter(model => model);

            // 收集偏好设置
            const preferencesTextarea = card.querySelector('.preferences');
            if (preferencesTextarea && preferencesTextarea.value.trim()) {
                try {
                    provider.preferences = JSON.parse(preferencesTextarea.value.trim());
                } catch (e) {
                    console.error('解析偏好设置JSON失败:', e);
                }
            }

            // 收集备注
            if (card.querySelector('.note')) {
                provider.notes = card.querySelector('.note').value;
            }

            providers.push(provider);
        });

        return { providers };
    }

    // 添加：测试连接并加载配置
    async function testConnectionAndLoadConfig() {
        try {
            await apiConnection.testConnection();
            connectionSettings.style.display = 'none';
            apiConfigArea.style.display = 'block';
            loadApiConfig();
        } catch (error) {
            console.error('连接服务器失败:', error);
            alert(`连接服务器失败: ${error.message}`);

            // 恢复连接设置显示
            connectionSettings.style.display = 'block';
            apiConfigArea.style.display = 'none';

            // 恢复按钮状态
            connectButton.disabled = false;
            connectButton.innerHTML = '连接服务器';
        }
    }

    // 初始化路由
    router.init();

    // 移动端点击内容区域时关闭侧边栏
    content.addEventListener('click', (e) => {
        if (isMobile() && !e.target.closest('a')) {
            sidebar.sidebar.classList.remove('expanded');
        }
    });
});
