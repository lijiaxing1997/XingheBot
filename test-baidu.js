const { chromium } = require('playwright');

(async () => {
  console.log('启动浏览器...');
  
  // 启动浏览器（无头模式）
  const browser = await chromium.launch({ 
    headless: true,
    args: ['--no-sandbox', '--disable-setuid-sandbox']
  });
  
  console.log('创建新页面...');
  const page = await browser.newPage();
  
  console.log('访问百度首页...');
  await page.goto('https://www.baidu.com', { 
    waitUntil: 'networkidle',
    timeout: 30000 
  });
  
  console.log('等待页面加载完成...');
  await page.waitForLoadState('domcontentloaded');
  
  // 获取页面标题
  const title = await page.title();
  console.log('页面标题:', title);
  
  // 截取截图
  const screenshotPath = '/Users/macbookpro/GoTools/AI/test_skill_agent/baidu-test.png';
  await page.screenshot({ 
    path: screenshotPath, 
    fullPage: true 
  });
  console.log('截图已保存到:', screenshotPath);
  
  // 关闭浏览器
  await browser.close();
  console.log('浏览器已关闭');
  
  console.log('\n=== 测试成功 ===');
  console.log('页面标题:', title);
  console.log('截图路径:', screenshotPath);
})();
