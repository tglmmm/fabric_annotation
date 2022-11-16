

> 包的功能注释  
* 与configtx.yml文件中 capabilities 配置相对应
* 定义了Application  Orderer Channel 版本 V_20: true，之所以这样定义是因为在不同版本中功能略有差异，差异化的处理过程被定义在了capabilities包中
* 包中包含 Provider接口，主要有方法 HasCapability , Type 两个方法， 前者验证当前的二进制是否支持传递的Capabilities类型，后者则返回当前接口是哪一类型（Channel , Application ,Orderer）
* 