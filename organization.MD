
##  Orderer 组织配置
```
├── ordererOrganizations
│   │
│   └── example.com
│       │
│       ├── ca      ##  存放组织根证书 及 私钥 (采用EC算法) 证书为【自签名】，组织内的实体将给予该根证书作为证书根
│       │
│       │   ├── 56d9c0c46acdda38a174a5ba3ffc44726a2c027e16bb22b460413acbcb9b3a90_sk
│       │   │
│       │   └── ca.example.com-cert.pem
│       │
│       ├── msp                 ##  存放该组织身份信息
│       │   │
│       │   ├── admincerts                              ##  组织管理员 身份验证证书，【被根证书签名】
│       │   │   │
│       │   │   └── Admin@example.com-cert.pem
│       │   │
│       │   ├── cacerts                                 ##  组织的根证书 【和CA目录 里面一致】
│       │   │   │
│       │   │   └── ca.example.com-cert.pem
│       │   │   
│       │   └── tlscacerts                              ##  用于TLS的CA证书， 【自签名】
│       │       │   
│       │       └── tlsca.example.com-cert.pem  
│       │
│       │
│       ├── orderers         ##  存放所有 Orderer 的身份信息
│       │   │
│       │   └── orderer.example.com                         ##  第一个 Orderer 的信息 msp 及 tls
│       │       │
│       │       ├── msp
│       │       │   │     
│       │       │   ├── admincerts                          ##  组织管理员的身份验证证书。Peer将给予这些证书来确认交易签名是否为管理员签名 【和MSP.admincerts 一致】
│       │       │   │   │
│       │       │   │   └── Admin@example.com-cert.pem
│       │       │   │
│       │       │   ├── cacerts
│       │       │   │   │
│       │       │   │   └── ca.example.com-cert.pem         ##  存放组织根证书，【和CA目录 里面一致】
│       │       │   │
│       │       │   ├── keystore                            ##  本节点的身份私钥，用来签名
│       │       │   │   │
│       │       │   │   └── 2ec1193fe048848eaa8e20666e26c527b791c4fb127d69cae65095bd31b6c80e_sk
│       │       │   │
│       │       │   ├── signcerts                           ##  验证本节点签名的证书，【被根证书签名】
│       │       │   │   │
│       │       │   │   └── orderer.example.com-cert.pem
│       │       │   │
│       │       │   └── tlscacerts                          ##  TLS连接用的身份证书， 【和msp.tlscacerts 一致】，与tls下的ca.crt一致
│       │       │       │
│       │       │       └── tlsca.example.com-cert.pem
│       │       │
│       │       └── tls             ##  tls 的相关信息
│       │           ├── ca.crt                              ##  【组织的根证书】 和根ca不同
│       │           ├── server.crt                          ##  验证本节点签名的证书， 【被根证书签名】
│       │           └── server.key                          ##  本节点的身份私钥，用来签名
│       │
│       ├── tlsca           ## 存放tls相关的证书和私钥
│       │   │
│       │   ├── 2d66be83c519da67bb36b0972256a3b24357fa7f5b3a61f11405bc8b1f4d7c53_sk
│       │   │
│       │   └── tlsca.example.com-cert.pem
│       │
│       └── users           ## 存放属于该组织的用户的实体
│           │
│           └── Admin@example.com                           ## 管理员用户的信息，其中包括msp证书和tls证书两类
│               │
│               ├── msp
│               │   │
│               │   ├── admincerts                          ## 组织根证书作为管理员身份验证证书，【和MSP.admincerts 一致】
│               │   │   │
│               │   │   └── Admin@example.com-cert.pem
│               │   │
│               │   ├── cacerts                              ## 存放组织的根证书，【和CA目录 里面一致】
│               │   │   │
│               │   │   └── ca.example.com-cert.pem
│               │   │
│               │   ├── keystore                            ## 本用户的身份私钥，用来签名
│               │   │   │
│               │   │   └── a3c1d7e1bc464faf2e3a205cb76ea231bd3ee7010655d3cd31dc6cb78726c4d0_sk
│               │   │
│               │   ├── signcerts                           ## 管理员用户的身份验证证书，被组织根证书签名。要被某个Orderer认可，则必须放到该 Orderer 的msp/admincerts目录下
│               │   │   │
│               │   │   └── Admin@example.com-cert.pem
│               │   │
│               │   └── tlscacerts                          ## TLS连接用的身份证书，即组织TLS证书，【和msp.tlscacerts 一致】
│               │       │
│               │       └── tlsca.example.com-cert.pem
│               │
│               │
│               └── tls         ##  tls 的相关信息
│                   ├── ca.crt
│                   ├── client.crt                          ##  管理员的身份验证证书，【被 组织根证书签名】
│                   └── client.key                          ##  管理员的身份私钥，用来签名
│
│
```
```
│   ##  Peer 组织配置
└── peerOrganizations
│
├── org1.example.com    ##  第一个组织的所有身份证书
│   │
│   ├── ca              ##  存放组织根证书及私钥 (采用EC算法) 证书为【自签名】，组织内的实体将给予该根证书作为证书根
│   │   │
│   │   ├── 496d6a41ae5f66bf120df3eab3a9d2dc4d268b2ab9a22af891d33d323bbdb5c8_sk
│   │   │
│   │   └── ca.org1.example.com-cert.pem
│   │
│   ├── msp             ##  存放该组织身份信息
│   │   │
│   │   ├── admincerts                              ##  组织管理员 身份验证证书，【被根证书签名】
│   │   │   │
│   │   │   └── Admin@org1.example.com-cert.pem
│   │   │
│   │   ├── cacerts                                 ##  组织的根证书 【和CA目录 里面一致】
│   │   │   │
│   │   │   └── ca.org1.example.com-cert.pem
│   │   │
│   │   ├── config.yaml                             ##  记录 OrganizationalUnitIdentitifiers 信息，包括 根证书位置 和 ID信息 (主要是 crypto-config.yaml 的peer配置中配了 EnableNodeOUs: true  ： 如果设置了EnableNodeOUs，就在msp下生成config.yaml文件)
│   │   │
│   │   └── tlscacerts                              ##  用于TLS的CA证书， 【自签名】
│   │       │
│   │       └── tlsca.org1.example.com-cert.pem
│   │
│   │
│   ├── peers           ##  存放所有 Peer 的身份信息
│   │   │
│   │   ├── peer0.org1.example.com                  ##  第一个Peer的信息 msp 及 tls
│   │   │   │
│   │   │   ├── msp
│   │   │   │   │
│   │   │   │   ├── admincerts                      ##  组织管理员的身份验证证书。Peer将给予这些证书来确认交易签名是否为管理员签名 【和MSP.admincerts 一致】
│   │   │   │   │   │
│   │   │   │   │   └── Admin@org1.example.com-cert.pem
│   │   │   │   │
│   │   │   │   ├── cacerts                         ##  存放组织根证书，【和CA目录 里面一致】
│   │   │   │   │   │
│   │   │   │   │   └── ca.org1.example.com-cert.pem
│   │   │   │   │
│   │   │   │   ├── config.yaml
│   │   │   │   │
│   │   │   │   ├── keystore                        ##  本节点的身份私钥，用来签名
│   │   │   │   │   │
│   │   │   │   │   └── 0f0c2e1835086161f6a10c4bb38c2d89b2cee4e1128cee0fcda4433feb6eb6f8_sk
│   │   │   │   │
│   │   │   │   │
│   │   │   │   ├── signcerts                       ##  验证本节点签名的证书，【被根证书签名】
│   │   │   │   │   │
│   │   │   │   │   └── peer0.org1.example.com-cert.pem
│   │   │   │   │
│   │   │   │   └── tlscacerts                      ##  TLS连接用的身份证书， 【和msp.tlscacerts 一致】
│   │   │   │       │
│   │   │   │       └── tlsca.org1.example.com-cert.pem
│   │   │   │
│   │   │   │
│   │   │   └──tls             ##  tls 的相关信息
│   │   │       ├── ca.crt                          ##  【组织的根证书】
│   │   │       ├── server.crt                      ##  验证本节点签名的证书， 【被根证书签名】
│   │   │       └── server.key                      ##  本节点的身份私钥，用来签名
│   │   │    
│   │   │    
│   │   └── peer1.org1.example.com
│   │
│   │
│   │
│   │       
│   ├── tlsca       ## 存放tls相关的证书和私钥
│   │   │
│   │   ├── 3d39ea82dd5343c261b0480bc13d645a3cee13b7e7aa8c54fd2b5162f709671f_sk
│   │   └── tlsca.org1.example.com-cert.pem
│   │
│   │
│   │
│   └── users       ## 存放属于该组织的用户的实体
│       │
│       ├── Admin@org1.example.com      ## 管理员用户的信息，其中包括msp证书和tls证书两类
│       │   │
│       │   ├── msp
│       │   │   │
│       │   │   ├── admincerts                      ## 组织根证书作为管理员身份验证证书，【和MSP.admincerts 一致】
│       │   │   │   │
│       │   │   │   └── Admin@org1.example.com-cert.pem
│       │   │   │
│       │   │   ├── cacerts                         ## 存放组织的根证书，【和CA目录 里面一致】
│       │   │   │   │
│       │   │   │   └── ca.org1.example.com-cert.pem
│       │   │   │
│       │   │   ├── keystore                        ## 本用户的身份私钥，用来签名
│       │   │   │   │
│       │   │   │   └── 2b933c0740d857284be98ff218bf279261e55eff2b89d973e0a1f435f7c7d28b_sk
│       │   │   │
│       │   │   ├── signcerts                       ## 管理员用户的身份验证证书，被组织根证书签名。要被某个Peer认可，则必须放到该Peer的msp/admincerts目录下
│       │   │   │   │
│       │   │   │   └── Admin@org1.example.com-cert.pem
│       │   │   │
│       │   │   └── tlscacerts                      ## TLS连接用的身份证书，即组织TLS证书，【和msp.tlscacerts 一致】
│       │   │       │
│       │   │       └── tlsca.org1.example.com-cert.pem
│       │   │
│       │   └── tls             ##  tls 的相关信息
│       │       ├── ca.crt
│       │       ├── client.crt                      ##  管理员的身份验证证书，【被 组织根证书签名】
│       │       └── client.key                      ##  管理员的身份私钥，用来签名
│       │
│       │
│       └── User1@org1.example.com
│           ├── msp
│           │   ├── admincerts                              ##  组织根证书作为管理员身份验证证书         
│           │   │   └── User1@org1.example.com-cert.pem
│           │   ├── cacerts                                 ##  存放组织的根证书，【和CA目录 里面一致】
│           │   │   └── ca.org1.example.com-cert.pem        
│           │   ├── keystore                                ##  【参考admin】
│           │   │   └── 11ebc5afac42348f84a8882f329d18beee079efd4fd5d9b30389dc82053fc0c9_sk
│           │   ├── signcerts                               ##  【参考admin】
│           │   │   └── User1@org1.example.com-cert.pem
│           │   └── tlscacerts                              ##  【参考admin】
│           │       └── tlsca.org1.example.com-cert.pem
│           └── tls             ##  【参考admin】
│               ├── ca.crt
│               ├── client.crt
│               └── client.key
│
└── org2.example.com
```