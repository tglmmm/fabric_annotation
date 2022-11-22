这里主要描述一个共识迁移的接口
包含两个方法
1.ReplaceGenesisBlockFile 替换 
2. CheckReadWrite 检查创世块文件的RW权限


读取指定文件（创世块文件），将文件装换成区块类型，备份原来的创世块文件，并将当前最新的创世块（proto.Marshal(block)）导出到文件